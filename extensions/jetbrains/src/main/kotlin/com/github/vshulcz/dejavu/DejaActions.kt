package com.github.vshulcz.dejavu

import com.intellij.notification.NotificationGroupManager
import com.intellij.notification.NotificationType
import com.intellij.openapi.actionSystem.AnAction
import com.intellij.openapi.actionSystem.AnActionEvent
import com.intellij.openapi.actionSystem.CommonDataKeys
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.fileEditor.FileEditorManager
import com.intellij.openapi.fileTypes.FileTypeManager
import com.intellij.openapi.ide.CopyPasteManager
import com.intellij.openapi.project.DumbAwareAction
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.Messages
import com.intellij.openapi.ui.popup.JBPopupFactory
import com.intellij.openapi.vfs.VfsUtilCore
import com.intellij.testFramework.LightVirtualFile
import java.awt.datatransfer.StringSelection
import java.io.File

object DejaUi {
    fun notify(project: Project?, content: String, type: NotificationType = NotificationType.INFORMATION) {
        NotificationGroupManager.getInstance()
            .getNotificationGroup("deja-vu")
            .createNotification("deja-vu", content, type)
            .notify(project)
    }

    fun ensureBinaryOrNotify(project: Project?): Boolean {
        if (DejaCli.findBinary() != null) return true
        notify(
            project,
            "deja binary not found. Install via `brew install vshulcz/tap/deja-vu` or https://github.com/vshulcz/deja-vu",
            NotificationType.WARNING,
        )
        return false
    }

    fun openMarkdownTab(project: Project, fileName: String, content: String) {
        ApplicationManager.getApplication().invokeLater {
            if (project.isDisposed) return@invokeLater
            val fileType = FileTypeManager.getInstance().getFileTypeByExtension("md")
            val vf = LightVirtualFile(fileName, fileType, content)
            vf.isWritable = false
            FileEditorManager.getInstance(project).openFile(vf, true)
        }
    }

    fun openSession(project: Project, item: DejaSessionItem) {
        ApplicationManager.getApplication().executeOnPooledThread {
            val args = buildList {
                add("show")
                add(item.id)
                if (item.harness.isNotEmpty() && item.harness != "agent") {
                    add("--harness")
                    add(item.harness)
                }
                add("--json")
            }
            val res = DejaCli.run(project, args)
            if (res == null || res.exitCode != 0 || res.stdout.isBlank()) {
                val plain = DejaCli.run(project, listOf("show", item.id))
                val body = plain?.stdout?.takeIf { it.isNotBlank() } ?: res?.stderr ?: "No transcript available."
                openMarkdownTab(project, "deja-${item.shortId}.md", DejaCli.stripAnsi(body))
                return@executeOnPooledThread
            }
            val md = DejaCli.formatSessionMarkdown(res.stdout, item)
            openMarkdownTab(project, "deja-${item.shortId}.md", md)
        }
    }

    fun showSessionChooser(project: Project, title: String, items: List<DejaSessionItem>) {
        ApplicationManager.getApplication().invokeLater {
            if (project.isDisposed) return@invokeLater
            if (items.isEmpty()) {
                notify(project, "No matching AI coding sessions found.")
                return@invokeLater
            }
            JBPopupFactory.getInstance()
                .createPopupChooserBuilder(items)
                .setTitle(title)
                .setRenderer { list, value, index, isSelected, cellHasFocus ->
                    javax.swing.DefaultListCellRenderer().getListCellRendererComponent(
                        list,
                        value.displayLabel(),
                        index,
                        isSelected,
                        cellHasFocus,
                    )
                }
                .setItemChosenCallback { chosen ->
                    openSession(project, chosen)
                }
                .setNamerForFiltering { it.displayLabel() }
                .createPopup()
                .showCenteredInCurrentWindow(project)
        }
    }
}

class DejaSearchAction : DumbAwareAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        if (!DejaUi.ensureBinaryOrNotify(project)) return
        val query = Messages.showInputDialog(
            project,
            "Search local sessions across 43 AI coding agents:",
            "deja-vu: Search AI Sessions",
            null,
        )?.trim() ?: return
        if (query.isEmpty()) return

        ApplicationManager.getApplication().executeOnPooledThread {
            val res = DejaCli.run(project, listOf("search", "--limit", "50", "--json", "--", query))
            if (res == null || res.exitCode != 0) {
                DejaUi.notify(project, res?.stderr?.trim()?.ifEmpty { "deja search failed" } ?: "deja search failed", NotificationType.WARNING)
                return@executeOnPooledThread
            }
            val items = DejaCli.parseSessions(res.stdout)
            DejaUi.showSessionChooser(project, "deja search: $query", items)
        }
    }
}

class DejaLastAction : DumbAwareAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        if (!DejaUi.ensureBinaryOrNotify(project)) return

        ApplicationManager.getApplication().executeOnPooledThread {
            val res = DejaCli.run(project, listOf("last", "50", "--json"))
            if (res == null || res.exitCode != 0) {
                DejaUi.notify(project, res?.stderr?.trim()?.ifEmpty { "deja last failed" } ?: "deja last failed", NotificationType.WARNING)
                return@executeOnPooledThread
            }
            val items = DejaCli.parseSessions(res.stdout)
            DejaUi.showSessionChooser(project, "deja: Recent AI Sessions", items)
        }
    }
}

class DejaBlameLineAction : DumbAwareAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        val editor = e.getData(CommonDataKeys.EDITOR)
        val vFile = e.getData(CommonDataKeys.VIRTUAL_FILE)
        if (editor == null || vFile == null) {
            DejaUi.notify(project, "Open a file in the editor to run AI Blame.", NotificationType.WARNING)
            return
        }
        if (!DejaUi.ensureBinaryOrNotify(project)) return

        val line = editor.caretModel.logicalPosition.line + 1
        val baseDir = project.basePath?.let { File(it) }
        val fileIo = VfsUtilCore.virtualToIoFile(vFile)
        val relPath = if (baseDir != null && fileIo.absolutePath.startsWith(baseDir.absolutePath + File.separator)) {
            fileIo.relativeTo(baseDir).invariantSeparatorsPath
        } else {
            vFile.path
        }
        val targetWithLine = "$relPath:$line"

        ApplicationManager.getApplication().executeOnPooledThread {
            var res = DejaCli.run(project, listOf("blame", targetWithLine, "--json"))
            var items = if (res != null && res.exitCode == 0) DejaCli.parseSessions(res.stdout) else emptyList()
            var label = targetWithLine
            if (items.isEmpty()) {
                res = DejaCli.run(project, listOf("blame", relPath, "--json"))
                items = if (res != null && res.exitCode == 0) DejaCli.parseSessions(res.stdout) else emptyList()
                label = relPath
            }
            if (items.size == 1) {
                DejaUi.openSession(project, items.first())
            } else {
                DejaUi.showSessionChooser(project, "deja blame: $label", items)
            }
        }
    }
}

class DejaHandoffAction : DumbAwareAction() {
    override fun actionPerformed(e: AnActionEvent) {
        val project = e.project ?: return
        if (!DejaUi.ensureBinaryOrNotify(project)) return

        ApplicationManager.getApplication().executeOnPooledThread {
            val res = DejaCli.run(project, listOf("handoff"))
            val text = res?.stdout?.let { DejaCli.stripAnsi(it) }?.trim().orEmpty()
            if (res == null || res.exitCode != 0 || text.isEmpty()) {
                DejaUi.notify(project, "No recent session handoff found for this project.", NotificationType.WARNING)
                return@executeOnPooledThread
            }
            ApplicationManager.getApplication().invokeLater {
                CopyPasteManager.getInstance().setContents(StringSelection(text))
                DejaUi.notify(project, "Copied cross-agent handoff digest to clipboard.")
            }
            DejaUi.openMarkdownTab(project, "deja-handoff.md", text)
        }
    }
}
