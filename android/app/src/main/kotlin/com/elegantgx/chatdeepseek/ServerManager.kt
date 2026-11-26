package com.elegantgx.chatdeepseek

import android.content.Context
import android.system.Os
import android.system.OsConstants
import java.io.File
import java.io.IOException
import java.net.HttpURLConnection
import java.net.InetAddress
import java.net.ServerSocket
import java.net.URL
import java.util.concurrent.TimeUnit

/**
 * Owns the bundled Go server process. The binary is shipped as
 * jniLibs/arm64-v8a/libchatdeepseek.so so Android extracts it to the
 * read-only nativeLibraryDir, the only place where exec is allowed on
 * Android 10+.
 */
class ServerManager(private val context: Context) {
  private val dataDir = File(context.filesDir, "data")
  private val logDir = File(context.filesDir, "logs")
  private val logFile = File(logDir, "server.log")

  private var process: Process? = null
  private var port: Int = 0

  val baseUrl: String
    get() = "http://127.0.0.1:$port/"

  fun isInitialized(): Boolean = File(dataDir, "chat.db").isFile

  @Synchronized
  fun setup(username: String, password: String) {
    ensureDirs()
    rotateLogIfNeeded()
    val p =
      ProcessBuilder(
          binaryFile().absolutePath,
          "setup",
          "--data-dir",
          dataDir.absolutePath,
          "--username",
          username,
          "--password-stdin",
        )
        .redirectErrorStream(true)
        .start()
    p.outputStream.bufferedWriter(Charsets.UTF_8).use { writer ->
      writer.write(password)
      writer.newLine()
    }
    val output = p.inputStream.bufferedReader(Charsets.UTF_8).readText()
    val code = p.waitFor()
    if (code != 0) {
      throw IOException(if (output.isBlank()) "创建账户失败（退出码 $code）" else output.trim())
    }
  }

  @Synchronized
  fun start(): String {
    val current = process
    if (current != null && current.isAlive && port > 0) return baseUrl
    ensureDirs()
    cleanupStaleProcess()
    rotateLogIfNeeded()

    port = freePort()
    val pb =
      ProcessBuilder(
        binaryFile().absolutePath,
        "serve",
        "--data-dir",
        dataDir.absolutePath,
        "--listen",
        "127.0.0.1:$port",
        "--no-browser",
      )
    pb.directory(context.filesDir)
    pb.redirectErrorStream(true)
    pb.environment()["TMPDIR"] = context.cacheDir.absolutePath
    pb.environment()["HOME"] = context.filesDir.absolutePath

    val p = pb.start()
    process = p
    startLogPump(p)
    if (!waitUntilReady(p)) {
      val tail = logTail()
      stop()
      throw IOException(if (tail.isBlank()) "本地服务启动失败" else "本地服务启动失败：\n$tail")
    }
    return baseUrl
  }

  @Synchronized
  fun stop() {
    val p = process
    process = null
    if (p != null) {
      runCatching { p.destroy() }
      runCatching {
        if (!p.waitFor(3, TimeUnit.SECONDS)) p.destroyForcibly()
      }
    }
    port = 0
  }

  @Synchronized
  fun resetData() {
    stop()
    runCatching { dataDir.deleteRecursively() }
  }

  fun logTail(): String {
    if (!logFile.isFile) return ""
    return runCatching { logFile.readText().takeLast(2000) }.getOrDefault("")
  }

  private fun ensureDirs() {
    dataDir.mkdirs()
    logDir.mkdirs()
  }

  private fun binaryFile(): File {
    val file = File(context.applicationInfo.nativeLibraryDir, "libchatdeepseek.so")
    if (!file.isFile) throw IOException("未找到内置服务端：${file.absolutePath}")
    return file
  }

  private fun freePort(): Int =
    ServerSocket(0, 1, InetAddress.getByName("127.0.0.1")).use { it.localPort }

  private fun startLogPump(p: Process) {
    Thread {
        runCatching {
          logFile.bufferedWriter(Charsets.UTF_8).use { writer ->
            p.inputStream.bufferedReader(Charsets.UTF_8).forEachLine { line ->
              writer.write(line)
              writer.newLine()
              writer.flush()
            }
          }
        }
      }
      .apply {
        isDaemon = true
        name = "chat-deepseek-log"
        start()
      }
  }

  private fun waitUntilReady(p: Process): Boolean {
    val deadline = System.currentTimeMillis() + 15_000
    while (System.currentTimeMillis() < deadline) {
      if (!p.isAlive) return false
      try {
        val conn = URL(baseUrl).openConnection() as HttpURLConnection
        conn.connectTimeout = 500
        conn.readTimeout = 500
        conn.requestMethod = "GET"
        val code = conn.responseCode
        runCatching { conn.inputStream?.close() }
        runCatching { conn.errorStream?.close() }
        conn.disconnect()
        if (code in 200..399) return true
      } catch (_: IOException) {
        // Server not listening yet.
      }
      Thread.sleep(100)
    }
    return false
  }

  /** Kills a server left behind by a previous app process, if any. */
  private fun cleanupStaleProcess() {
    val myPid = android.os.Process.myPid()
    val proc = File("/proc")
    proc.listFiles()?.forEach { dir ->
      val pid = dir.name.toIntOrNull() ?: return@forEach
      if (pid == myPid) return@forEach
      val cmdline =
        runCatching { File(dir, "cmdline").readText() }.getOrNull() ?: return@forEach
      if (!cmdline.contains("libchatdeepseek.so")) return@forEach
      runCatching { Os.kill(pid, OsConstants.SIGTERM) }
      Thread.sleep(200)
      if (File(dir.path).exists()) {
        runCatching { Os.kill(pid, OsConstants.SIGKILL) }
      }
    }
  }

  private fun rotateLogIfNeeded() {
    if (logFile.isFile && logFile.length() > 1_000_000) {
      runCatching { logFile.delete() }
    }
  }
}
