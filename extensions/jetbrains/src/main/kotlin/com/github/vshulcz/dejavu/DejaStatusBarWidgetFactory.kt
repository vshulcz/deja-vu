package com.github.vshulcz.dejavu

import com.intellij.openapi.actionSystem.ActionManager
import com.intellij.openapi.actionSystem.DefaultActionGroup
import com.intellij.openapi.actionSystem.impl.SimpleDataContext
import com.intellij.openapi.application.ApplicationManager
import com.intellij.openapi.fileEditor.FileEditorManagerEvent
import com.intellij.openapi.fileEditor.FileEditorManagerListener
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.popup.JBPopupFactory
import com.intellij.openapi.wm.StatusBar
import com.intellij.openapi.wm.StatusBarWidget
import com.intellij.openapi.wm.StatusBarWidgetFactory
import com.intellij.ui.awt.RelativePoint
import com.intellij.util.Alarm
import com.intellij.util.Consumer
import java.awt.event.MouseEvent

class DejaStatusBarWidgetFactory : StatusBarWidgetFactory {
    override fun getId(): String = WIDGET_ID
    override fun getDisplayName(): String = "deja-vu AI Session Status"
    override fun isAvailable(project: Project): Boolean = true
    override fun createWidget(project: Project): StatusBarWidget = DejaStatusBarWidget(project)

    companion object {
        const val WIDGET_ID = "com.github.vshulcz.dejavu.statusBar"
    }
}

class DejaStatusBarWidget(private val project: Project) : StatusBarWidget, StatusBarWidget.TextPresentation {
    private var statusBar: StatusBar? = null
    private val alarm = Alarm(Alarm.ThreadToUse.POOLED_THREAD, this)

    @Volatile
    private var currentText: String = "deja"

    @Volatile
    private var currentTooltip: String = "deja-vu — Cross-agent AI session recall (click for actions)"

    override fun ID(): String = DejaStatusBarWidgetFactory.WIDGET_ID

    override fun getPresentation(): StatusBarWidget.WidgetPresentation = this

    override fun install(statusBar: StatusBar) {
        this.statusBar = statusBar
        project.messageBus.connect(this).subscribe(
            FileEditorManagerListener.FILE_EDITOR_MANAGER,
            object : FileEditorManagerListener {
                override fun selectionChanged(event: FileEditorManagerEvent) {
                    scheduleRefresh(150)
                }
            },
        )
        scheduleRefresh(100)
    }

    private fun scheduleRefresh(delayMs: Int) {
        if (project.isDisposed) return
        alarm.cancelAllRequests()
        alarm.addRequest({
            if (project.isDisposed) return@addRequest
            val bin = DejaCli.findBinary()
            if (bin == null) {
                currentText = "deja: install"
                currentTooltip = "deja binary not found — click for setup instructions"
            } else {
                val status = DejaCli.statusline(project)
                currentText = status ?: "deja"
                currentTooltip = "deja-vu — click to search AI sessions, blame line, or copy handoff"
            }
            ApplicationManager.getApplication().invokeLater {
                statusBar?.updateWidget(ID())
            }
            if (!project.isDisposed) {
                scheduleRefresh(15_000)
            }
        }, delayMs)
    }

    override fun getText(): String = currentText

    override fun getAlignment(): Float = 0.5f

    override fun getTooltipText(): String = currentTooltip

    override fun getClickConsumer(): Consumer<MouseEvent> = Consumer { event ->
        if (!DejaUi.ensureBinaryOrNotify(project)) return@Consumer
        val am = ActionManager.getInstance()
        val group = (am.getAction("DejaVu.Menu") as? DefaultActionGroup) ?: DefaultActionGroup().apply {
            am.getAction("DejaVu.SearchSessions")?.let { add(it) }
            am.getAction("DejaVu.RecentSessions")?.let { add(it) }
            am.getAction("DejaVu.BlameLine")?.let { add(it) }
            am.getAction("DejaVu.CopyHandoff")?.let { add(it) }
        }
        val dataContext = SimpleDataContext.getProjectContext(project)
        val popup = JBPopupFactory.getInstance().createActionGroupPopup(
            "deja-vu",
            group,
            dataContext,
            JBPopupFactory.ActionSelectionAid.SPEEDSEARCH,
            false,
        )
        popup.show(RelativePoint(event))
    }

    override fun dispose() {
        alarm.cancelAllRequests()
        statusBar = null
    }
}
