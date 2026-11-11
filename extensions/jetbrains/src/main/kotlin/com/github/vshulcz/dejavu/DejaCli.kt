package com.github.vshulcz.dejavu

import com.google.gson.JsonElement
import com.google.gson.JsonObject
import com.google.gson.JsonParser
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.configurations.PathEnvironmentVariableUtil
import com.intellij.execution.util.ExecUtil
import com.intellij.openapi.project.Project
import java.io.File
import java.nio.charset.StandardCharsets

data class DejaSessionItem(
    val id: String,
    val shortId: String,
    val harness: String,
    val project: String,
    val title: String,
    val updated: String,
    val snippet: String,
) {
    fun displayLabel(): String {
        val proj = if (project.isNotEmpty()) " · $project" else ""
        val ts = if (updated.isNotEmpty()) " [$updated]" else ""
        val summary = snippet.ifEmpty { title }
        return "[$harness] $shortId$proj$ts — $summary"
    }
}

data class DejaRunResult(
    val exitCode: Int,
    val stdout: String,
    val stderr: String,
)

object DejaCli {
    private val ansiRegex = Regex("\u001B\\[[0-9;]*[A-Za-z]")
    private val isWindows = System.getProperty("os.name", "").lowercase().contains("win")

    fun findBinary(): String? {
        val envBin = System.getenv("DEJA_BIN")?.trim()
        if (!envBin.isNullOrEmpty() && File(envBin).canExecute()) {
            return envBin
        }

        val home = System.getProperty("user.home") ?: ""
        val ext = if (isWindows) ".exe" else ""
        val candidates = buildList {
            if (home.isNotEmpty()) {
                add(File(home, ".config/deja/bin/deja-hook$ext"))
                add(File(home, ".local/bin/deja$ext"))
                add(File(home, "go/bin/deja$ext"))
                add(File(home, ".cargo/bin/deja$ext"))
                if (isWindows) {
                    val localAppData = System.getenv("LOCALAPPDATA")
                    if (!localAppData.isNullOrEmpty()) {
                        add(File(localAppData, "Programs/deja/deja.exe"))
                    }
                }
            }
            if (!isWindows) {
                add(File("/opt/homebrew/bin/deja"))
                add(File("/usr/local/bin/deja"))
                add(File("/home/linuxbrew/.linuxbrew/bin/deja"))
                add(File("/usr/bin/deja"))
            }
        }
        for (file in candidates) {
            if (file.isFile && file.canExecute()) {
                return file.absolutePath
            }
        }

        val fromPath = PathEnvironmentVariableUtil.findInPath("deja$ext")
        return fromPath?.takeIf { it.canExecute() }?.absolutePath
    }

    fun stripAnsi(text: String): String = ansiRegex.replace(text, "").trim()

    fun run(project: Project?, args: List<String>, stdinJson: String? = null, timeoutMs: Int = 10_000): DejaRunResult? {
        val bin = findBinary() ?: return null
        val cmd = GeneralCommandLine(bin)
            .withParameters(args)
            .withCharset(StandardCharsets.UTF_8)
        val basePath = project?.basePath
        if (!basePath.isNullOrEmpty() && File(basePath).isDirectory) {
            cmd.withWorkDirectory(basePath)
        }

        return try {
            if (stdinJson != null) {
                val process = cmd.createProcess()
                process.outputStream.bufferedWriter(StandardCharsets.UTF_8).use { writer ->
                    writer.write(stdinJson)
                }
                val stdout = process.inputStream.bufferedReader(StandardCharsets.UTF_8).readText()
                val stderr = process.errorStream.bufferedReader(StandardCharsets.UTF_8).readText()
                process.waitFor()
                DejaRunResult(process.exitValue(), stdout, stderr)
            } else {
                val output = ExecUtil.execAndGetOutput(cmd, timeoutMs)
                DejaRunResult(output.exitCode, output.stdout, output.stderr)
            }
        } catch (_: Exception) {
            null
        }
    }

    fun statusline(project: Project?): String? {
        val cwd = project?.basePath ?: ""
        val escaped = cwd.replace("\\", "\\\\").replace("\"", "\\\"")
        val res = run(project, listOf("statusline"), stdinJson = "{\"cwd\":\"$escaped\"}", timeoutMs = 3_000) ?: return null
        if (res.exitCode != 0) return null
        return stripAnsi(res.stdout).lineSequence().firstOrNull()?.trim()?.takeIf { it.isNotEmpty() }
    }

    fun parseSessions(jsonText: String): List<DejaSessionItem> {
        val trimmed = jsonText.trim()
        if (trimmed.isEmpty()) return emptyList()
        val root = try {
            JsonParser.parseString(trimmed)
        } catch (_: Exception) {
            return emptyList()
        }
        val array = when {
            root.isJsonArray -> root.asJsonArray
            root.isJsonObject -> {
                val obj = root.asJsonObject
                when {
                    obj.has("hits") && obj.get("hits").isJsonArray -> obj.getAsJsonArray("hits")
                    obj.has("sessions") && obj.get("sessions").isJsonArray -> obj.getAsJsonArray("sessions")
                    else -> null
                }
            }
            else -> null
        } ?: return emptyList()

        val items = mutableListOf<DejaSessionItem>()
        for (elem in array) {
            if (!elem.isJsonObject) continue
            val rawObj = elem.asJsonObject
            val s = if (rawObj.has("session") && rawObj.get("session").isJsonObject) {
                rawObj.getAsJsonObject("session")
            } else {
                rawObj
            }
            val id = s.stringField("id")
            if (id.isEmpty()) continue
            val harness = s.stringField("harness").ifEmpty { "agent" }
            val proj = s.stringField("project")
            val rawUpdated = s.stringField("updated")
            val updated = if (rawUpdated.length >= 16) rawUpdated.substring(0, 16).replace('T', ' ') else rawUpdated
            val title = s.stringField("title").ifEmpty { id.take(12) }
            val hitSnippet = if (rawObj.has("snippets") && rawObj.get("snippets").isJsonArray) {
                rawObj.getAsJsonArray("snippets").firstOrNull()?.asStringOrNull().orEmpty()
            } else {
                rawObj.stringField("snippet")
            }
            val snippet = hitSnippet
                .ifEmpty { s.stringField("snippet") }
                .ifEmpty { firstMessageSnippet(s) }
                .replace(Regex("\\s+"), " ")
                .take(140)
            items.add(
                DejaSessionItem(
                    id = id,
                    shortId = id.take(8),
                    harness = harness,
                    project = proj,
                    title = title,
                    updated = updated,
                    snippet = snippet,
                )
            )
        }
        return items
    }

    fun formatSessionMarkdown(jsonOrText: String, fallbackItem: DejaSessionItem): String {
        val root = try {
            JsonParser.parseString(jsonOrText.trim())
        } catch (_: Exception) {
            return jsonOrText
        }
        if (!root.isJsonObject) return jsonOrText
        val env = root.asJsonObject
        val s = if (env.has("session") && env.get("session").isJsonObject) {
            env.getAsJsonObject("session")
        } else {
            env
        }
        val title = s.stringField("title").ifEmpty { fallbackItem.title }
        val harness = s.stringField("harness").ifEmpty { fallbackItem.harness }
        val id = s.stringField("id").ifEmpty { fallbackItem.id }
        val proj = s.stringField("project")
        val updated = s.stringField("updated")

        val sb = StringBuilder()
        sb.appendLine("# $title")
        sb.appendLine()
        sb.appendLine("- **Agent:** `$harness`")
        sb.appendLine("- **Session ID:** `$id`")
        if (proj.isNotEmpty()) sb.appendLine("- **Project:** `$proj`")
        if (updated.isNotEmpty()) sb.appendLine("- **Updated:** `$updated`")
        if (env.has("touched") && env.get("touched").isJsonArray) {
            val touched = env.getAsJsonArray("touched").mapNotNull { it.asStringOrNull() }.filter { it.isNotEmpty() }
            if (touched.isNotEmpty()) {
                sb.appendLine("- **Files touched (${touched.size}):** `${touched.joinToString("`, `")}`")
            }
        }
        sb.appendLine()
        sb.appendLine("---")

        if (s.has("messages") && s.get("messages").isJsonArray) {
            for (mElem in s.getAsJsonArray("messages")) {
                if (!mElem.isJsonObject) continue
                val m = mElem.asJsonObject
                val role = m.stringField("role").ifEmpty { "message" }.uppercase()
                val time = m.stringField("time")
                val hdrTime = if (time.length >= 19) " (${time.substring(0, 19).replace('T', ' ')})" else ""
                sb.appendLine()
                sb.appendLine("### $role$hdrTime")
                sb.appendLine()
                sb.appendLine(m.stringField("text"))
            }
        }
        return sb.toString()
    }

    private fun firstMessageSnippet(s: JsonObject): String {
        if (!s.has("messages") || !s.get("messages").isJsonArray) return ""
        for (m in s.getAsJsonArray("messages")) {
            if (!m.isJsonObject) continue
            val text = m.asJsonObject.stringField("text").trim()
            if (text.isNotEmpty()) return text
        }
        return ""
    }

    private fun JsonObject.stringField(key: String): String {
        val el = this.get(key) ?: return ""
        return el.asStringOrNull() ?: ""
    }

    private fun JsonElement.asStringOrNull(): String? =
        if (this.isJsonPrimitive && this.asJsonPrimitive.isString) this.asString else null
}
