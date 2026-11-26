package com.elegantgx.chatdeepseek

import android.Manifest
import android.app.DownloadManager
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.Configuration
import android.graphics.Color
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.text.InputType
import android.util.Log
import android.util.TypedValue
import android.view.Gravity
import android.view.View
import android.view.ViewGroup
import android.view.WindowManager
import android.webkit.ConsoleMessage
import android.webkit.CookieManager
import android.webkit.PermissionRequest
import android.webkit.URLUtil
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.Button
import android.widget.EditText
import android.widget.FrameLayout
import android.widget.LinearLayout
import android.widget.ProgressBar
import android.widget.ScrollView
import android.widget.TextView
import android.widget.Toast
import androidx.activity.ComponentActivity
import androidx.activity.OnBackPressedCallback
import androidx.activity.enableEdgeToEdge
import androidx.activity.result.contract.ActivityResultContracts
import androidx.core.content.ContextCompat
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import java.util.concurrent.Executors

class MainActivity : ComponentActivity() {
  companion object {
    private const val TAG = "ChatDeepseek"
  }

  private lateinit var server: ServerManager
  private lateinit var root: FrameLayout
  private val executor = Executors.newSingleThreadExecutor()
  private val mainHandler = Handler(Looper.getMainLooper())

  private var webView: WebView? = null
  private var pendingFileCallback: ValueCallback<Array<Uri>>? = null
  private var pendingPermission: PermissionRequest? = null

  private val filePicker =
    registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
      val callback = pendingFileCallback
      pendingFileCallback = null
      val uris =
        WebChromeClient.FileChooserParams.parseResult(result.resultCode, result.data)
      Log.d(
        TAG,
        "filePicker resultCode=${result.resultCode} data=${result.data != null} uris=${uris?.size ?: 0}",
      )
      callback?.onReceiveValue(uris)
    }

  private val micPermission =
    registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
      val request = pendingPermission
      pendingPermission = null
      Log.d(TAG, "micPermission granted=$granted pendingRequest=${request != null}")
      if (request != null) {
        if (granted) request.grant(request.resources) else request.deny()
      }
    }

  private val notificationsPermission =
    registerForActivityResult(ActivityResultContracts.RequestPermission()) { granted ->
      Log.d(TAG, "notificationsPermission granted=$granted")
    }

  override fun onCreate(savedInstanceState: Bundle?) {
    super.onCreate(savedInstanceState)
    server = (application as ChatApp).server
    enableEdgeToEdge()

    root = FrameLayout(this)
    root.setBackgroundColor(if (isNight()) Color.rgb(15, 17, 21) else Color.WHITE)
    setContentView(root)
    window.setSoftInputMode(WindowManager.LayoutParams.SOFT_INPUT_ADJUST_RESIZE)
    WindowCompat.getInsetsController(window, root).isAppearanceLightStatusBars = !isNight()

    ViewCompat.setOnApplyWindowInsetsListener(root) { view, insets ->
      val bars =
        insets.getInsets(
          WindowInsetsCompat.Type.systemBars() or WindowInsetsCompat.Type.displayCutout()
        )
      val ime = insets.getInsets(WindowInsetsCompat.Type.ime())
      view.setPadding(bars.left, bars.top, bars.right, maxOf(bars.bottom, ime.bottom))
      insets
    }

    onBackPressedDispatcher.addCallback(
      this,
      object : OnBackPressedCallback(true) {
        override fun handleOnBackPressed() {
          val wv = webView
          if (wv != null && wv.canGoBack()) {
            wv.goBack()
          } else if (isTaskRoot) {
            moveTaskToBack(true)
          } else {
            finish()
          }
        }
      },
    )

    if (server.isInitialized()) {
      startServer()
    } else {
      showSetup(null)
    }
  }

  private fun isNight(): Boolean =
    (resources.configuration.uiMode and Configuration.UI_MODE_NIGHT_MASK) ==
      Configuration.UI_MODE_NIGHT_YES

  private fun ensureServerService() {
    if (
      Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU &&
        ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) !=
          PackageManager.PERMISSION_GRANTED
    ) {
      notificationsPermission.launch(Manifest.permission.POST_NOTIFICATIONS)
    }
    ServerService.start(this)
  }

  private fun showSetup(error: String?) {
    destroyWebView()
    root.removeAllViews()

    val content =
      LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER_HORIZONTAL
        setPadding(dp(24), dp(40), dp(24), dp(24))
      }

    content.addView(
      TextView(this).apply {
        text = "chat-deepseek"
        textSize = 30f
        gravity = Gravity.CENTER
      }
    )
    content.addView(
      TextView(this).apply {
        text = "首次使用：创建 Owner 账户"
        textSize = 16f
        gravity = Gravity.CENTER
        setPadding(0, dp(8), 0, dp(12))
      }
    )

    val username = field("用户名", false).apply { setText("owner") }
    val password = field("密码（至少 8 位）", true)
    val confirm = field("再次输入密码", true)
    val errorView =
      TextView(this).apply {
        textSize = 14f
        setTextColor(Color.rgb(179, 38, 30))
        visibility = if (error.isNullOrBlank()) View.GONE else View.VISIBLE
        text = error.orEmpty()
        setPadding(0, dp(8), 0, 0)
      }
    val progress = ProgressBar(this).apply { visibility = View.GONE }
    val submit =
      Button(this).apply {
        text = "创建 Owner 并进入"
        layoutParams =
          LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
            .apply { topMargin = dp(16) }
      }

    content.addView(username)
    content.addView(password)
    content.addView(confirm)
    content.addView(errorView)
    content.addView(progress)
    content.addView(submit)
    content.addView(
      TextView(this).apply {
        text = "密码没有找回功能，请自行牢记。数据只保存在本机应用私有目录。"
        textSize = 12f
        setPadding(0, dp(16), 0, 0)
      }
    )

    submit.setOnClickListener {
      val user = username.text.toString().trim()
      val pass = password.text.toString()
      val again = confirm.text.toString()
      val problem =
        when {
          !Regex("^[A-Za-z0-9._-]{1,64}$").matches(user) ->
            "用户名只能使用 1-64 位字母、数字或 . _ -"
          pass.length < 8 -> "密码至少 8 位"
          pass != again -> "两次输入的密码不一致"
          else -> null
        }
      if (problem != null) {
        errorView.text = problem
        errorView.visibility = View.VISIBLE
        return@setOnClickListener
      }
      submit.isEnabled = false
      progress.visibility = View.VISIBLE
      errorView.visibility = View.GONE
      executor.execute {
        try {
          server.setup(user, pass)
          mainHandler.post {
            if (!isFinishing && !isDestroyed) {
              startServer()
            }
          }
        } catch (e: Exception) {
          server.resetData()
          val message = e.message ?: "创建账户失败"
          mainHandler.post {
            if (!isFinishing && !isDestroyed) {
              showSetup(message)
            }
          }
        }
      }
    }

    val scroll =
      ScrollView(this).apply {
        isFillViewport = true
        addView(content)
      }
    root.addView(
      scroll,
      FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT),
    )
  }

  private fun startServer() {
    showLoading("正在启动本地服务…")
    ensureServerService()
    executor.execute {
      try {
        val url = server.start()
        mainHandler.post {
          if (!isFinishing && !isDestroyed) {
            showWebView(url)
          }
        }
      } catch (e: Exception) {
        val message = e.message ?: "本地服务启动失败"
        mainHandler.post {
          if (!isFinishing && !isDestroyed) {
            showServerError(message)
          }
        }
      }
    }
  }

  private fun showLoading(message: String) {
    destroyWebView()
    root.removeAllViews()
    val box =
      LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        gravity = Gravity.CENTER
        setPadding(dp(24), dp(24), dp(24), dp(24))
      }
    box.addView(ProgressBar(this))
    box.addView(
      TextView(this).apply {
        text = message
        textSize = 15f
        setPadding(0, dp(12), 0, 0)
        gravity = Gravity.CENTER
      }
    )
    root.addView(
      box,
      FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT),
    )
  }

  private fun showServerError(message: String) {
    destroyWebView()
    root.removeAllViews()
    val content =
      LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(20), dp(32), dp(20), dp(20))
      }
    content.addView(
      TextView(this).apply {
        text = "无法启动本地服务"
        textSize = 22f
      }
    )
    content.addView(
      TextView(this).apply {
        text = message
        textSize = 13f
        setTextIsSelectable(true)
        setPadding(0, dp(12), 0, dp(16))
      }
    )
    content.addView(
      Button(this).apply {
        text = "重试"
        setOnClickListener { startServer() }
      }
    )
    root.addView(
      ScrollView(this).apply { addView(content) },
      FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT),
    )
  }

  private fun showWebView(url: String) {
    val wv =
      webView
        ?: WebView(this)
          .also {
            webView = it
            configureWebView(it)
          }
    root.removeAllViews()
    root.addView(
      wv,
      FrameLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.MATCH_PARENT),
    )
    if (wv.url != url) {
      wv.loadUrl(url)
    }
  }

  private fun configureWebView(wv: WebView) {
    if (BuildConfig.DEBUG) {
      WebView.setWebContentsDebuggingEnabled(true)
    }
    wv.setBackgroundColor(if (isNight()) Color.rgb(15, 17, 21) else Color.WHITE)
    CookieManager.getInstance().setAcceptCookie(true)
    CookieManager.getInstance().setAcceptThirdPartyCookies(wv, false)

    wv.settings.apply {
      javaScriptEnabled = true
      domStorageEnabled = true
      databaseEnabled = true
      allowFileAccess = false
      allowContentAccess = true
      mediaPlaybackRequiresUserGesture = false
      mixedContentMode = WebSettings.MIXED_CONTENT_NEVER_ALLOW
      cacheMode = WebSettings.LOAD_DEFAULT
      setSupportMultipleWindows(false)
    }

    wv.webViewClient =
      object : WebViewClient() {
        override fun shouldOverrideUrlLoading(
          view: WebView?,
          request: WebResourceRequest?,
        ): Boolean {
          val uri = request?.url ?: return false
          val host = uri.host
          if (host == "127.0.0.1" || host == "localhost") return false
          return try {
            startActivity(Intent(Intent.ACTION_VIEW, uri))
            true
          } catch (_: Exception) {
            true
          }
        }

        override fun onPageFinished(view: WebView?, url: String?) {
          super.onPageFinished(view, url)
          Log.d(TAG, "onPageFinished $url")
          if (!BuildConfig.DEBUG) {
            return
          }
          val js =
            """
            (function(){
              try {
                var rate = null, ctxErr = '';
                try {
                  var AC = window.AudioContext || window.webkitAudioContext;
                  var ac = new AC({sampleRate: 16000});
                  rate = ac.sampleRate;
                  ac.close();
                } catch (e) { ctxErr = e.name + ':' + e.message; }
                console.log('diag ' + JSON.stringify({
                  secure: window.isSecureContext,
                  origin: location.origin,
                  mediaDevices: !!navigator.mediaDevices,
                  getUserMedia: !!(navigator.mediaDevices && navigator.mediaDevices.getUserMedia),
                  audioContext: typeof (window.AudioContext || window.webkitAudioContext),
                  scriptProcessor: !!(window.AudioContext && AudioContext.prototype.createScriptProcessor),
                  mediaRecorder: typeof MediaRecorder,
                  fileInputs: document.querySelectorAll('input[type=file]').length,
                  audioCtxRate: rate,
                  audioCtxErr: ctxErr,
                  voiceButtons: Array.from(document.querySelectorAll('button')).filter(function(b){
                    var l = b.getAttribute('aria-label') || '';
                    return l.indexOf('语音') >= 0 || l.indexOf('麦克风') >= 0;
                  }).map(function(b){ return {label: b.getAttribute('aria-label'), disabled: !!b.disabled}; })
                }));
              } catch (e) { console.log('diag error ' + e); }
            })();
            """
              .trimIndent()
          view?.evaluateJavascript(js, null)
        }

        override fun onReceivedError(
          view: WebView?,
          request: WebResourceRequest?,
          error: WebResourceError?,
        ) {
          if (request?.isForMainFrame == true) {
            Log.e(TAG, "page error ${error?.errorCode}: ${error?.description}")
            Toast.makeText(
                this@MainActivity,
                "页面加载失败：${error?.description ?: "未知错误"}",
                Toast.LENGTH_LONG,
              )
              .show()
          }
        }
      }

    wv.webChromeClient =
      object : WebChromeClient() {
        override fun onPermissionRequest(request: PermissionRequest?) {
          if (request == null) return
          Log.d(
            TAG,
            "onPermissionRequest origin=${request.origin} resources=${request.resources.joinToString()}",
          )
          val needsAudio = request.resources.contains(PermissionRequest.RESOURCE_AUDIO_CAPTURE)
          if (!needsAudio) {
            request.deny()
            return
          }
          if (
            ContextCompat.checkSelfPermission(this@MainActivity, Manifest.permission.RECORD_AUDIO) ==
              PackageManager.PERMISSION_GRANTED
          ) {
            request.grant(arrayOf(PermissionRequest.RESOURCE_AUDIO_CAPTURE))
          } else {
            pendingPermission?.deny()
            pendingPermission = request
            micPermission.launch(Manifest.permission.RECORD_AUDIO)
          }
        }

        override fun onPermissionRequestCanceled(request: PermissionRequest?) {
          Log.d(TAG, "onPermissionRequestCanceled origin=${request?.origin}")
          if (request != null && pendingPermission === request) {
            pendingPermission = null
          }
        }

        override fun onShowFileChooser(
          webView: WebView?,
          filePathCallback: ValueCallback<Array<Uri>>?,
          fileChooserParams: WebChromeClient.FileChooserParams?,
        ): Boolean {
          if (filePathCallback == null) return false
          val accepted = fileChooserParams?.acceptTypes?.filter { it.isNotBlank() } ?: emptyList()
          Log.d(
            TAG,
            "onShowFileChooser mode=${fileChooserParams?.mode} accept=${accepted.joinToString()}",
          )
          pendingFileCallback?.onReceiveValue(null)
          pendingFileCallback = filePathCallback

          // WebView 的 accept 可能是扩展名（.pdf,.docx）而不是 MIME。
          // EXTRA_MIME_TYPES 只接受 MIME，乱传会让系统文件选择器过滤掉所有文件。
          val mimeTypes = accepted.filter { it.contains("/") }
          val intent =
            Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
              addCategory(Intent.CATEGORY_OPENABLE)
              type = mimeTypes.firstOrNull() ?: "*/*"
              if (mimeTypes.isNotEmpty()) {
                putExtra(Intent.EXTRA_MIME_TYPES, mimeTypes.toTypedArray())
              }
              putExtra(
                Intent.EXTRA_ALLOW_MULTIPLE,
                fileChooserParams?.mode == WebChromeClient.FileChooserParams.MODE_OPEN_MULTIPLE,
              )
            }
          return try {
            filePicker.launch(intent)
            true
          } catch (e: Exception) {
            Log.e(TAG, "file chooser launch failed", e)
            pendingFileCallback = null
            false
          }
        }

        override fun onConsoleMessage(msg: ConsoleMessage?): Boolean {
          if (msg != null) {
            Log.d(TAG, "console ${msg.messageLevel()} ${msg.message()} @${msg.sourceId()}:${msg.lineNumber()}")
          }
          return true
        }
      }

    wv.setDownloadListener { url, _, contentDisposition, mimeType, _ ->
      if (url.startsWith("http://") || url.startsWith("https://")) {
        try {
          val fileName = URLUtil.guessFileName(url, contentDisposition, mimeType)
          val request =
            DownloadManager.Request(Uri.parse(url)).apply {
              CookieManager.getInstance().getCookie(url)?.let { addRequestHeader("Cookie", it) }
              setTitle(fileName)
              setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
              setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, fileName)
            }
          getSystemService(DownloadManager::class.java).enqueue(request)
          Toast.makeText(this, "开始下载：$fileName", Toast.LENGTH_SHORT).show()
        } catch (e: Exception) {
          Toast.makeText(this, "下载失败：${e.message}", Toast.LENGTH_LONG).show()
        }
      } else {
        Toast.makeText(this, "当前版本暂不支持直接保存该文件", Toast.LENGTH_LONG).show()
      }
    }
  }

  private fun field(hint: String, password: Boolean): EditText =
    EditText(this).apply {
      this.hint = hint
      inputType =
        if (password) {
          InputType.TYPE_CLASS_TEXT or InputType.TYPE_TEXT_VARIATION_PASSWORD
        } else {
          InputType.TYPE_CLASS_TEXT
        }
      setSingleLine(true)
      textSize = 16f
      layoutParams =
        LinearLayout.LayoutParams(ViewGroup.LayoutParams.MATCH_PARENT, ViewGroup.LayoutParams.WRAP_CONTENT)
          .apply { topMargin = dp(12) }
    }

  private fun dp(value: Int): Int =
    TypedValue.applyDimension(TypedValue.COMPLEX_UNIT_DIP, value.toFloat(), resources.displayMetrics)
      .toInt()

  private fun destroyWebView() {
    val wv = webView ?: return
    (wv.parent as? ViewGroup)?.removeView(wv)
    wv.destroy()
    webView = null
  }

  override fun onPause() {
    webView?.onPause()
    CookieManager.getInstance().flush()
    super.onPause()
  }

  override fun onResume() {
    super.onResume()
    webView?.onResume()
  }

  override fun onDestroy() {
    destroyWebView()
    executor.shutdownNow()
    super.onDestroy()
  }
}
