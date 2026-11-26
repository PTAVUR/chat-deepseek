package com.elegantgx.chatdeepseek

import android.Manifest
import android.app.Activity
import android.app.DownloadManager
import android.content.ClipData
import android.content.ContentResolver
import android.content.ContentUris
import android.content.Intent
import android.content.pm.PackageManager
import android.content.res.ColorStateList
import android.content.res.Configuration
import android.graphics.Bitmap
import android.graphics.Color
import android.graphics.drawable.GradientDrawable
import android.graphics.drawable.RippleDrawable
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.os.Handler
import android.os.Looper
import android.provider.MediaStore
import android.provider.Settings
import android.text.InputType
import android.util.Log
import android.util.Size
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
import android.widget.HorizontalScrollView
import android.widget.ImageView
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
import androidx.core.content.FileProvider
import androidx.core.view.ViewCompat
import androidx.core.view.WindowCompat
import androidx.core.view.WindowInsetsCompat
import java.io.File
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
  private var pendingCameraUri: Uri? = null
  private var pendingCameraFile: File? = null

  private var attachmentSheet: LinearLayout? = null
  private var attachmentScrim: View? = null
  private var attachmentRecentSection: LinearLayout? = null
  private var attachmentRecentRow: LinearLayout? = null
  private var attachmentRecentHint: TextView? = null
  private var attachmentHeader: LinearLayout? = null
  private var attachmentHeaderCount: TextView? = null
  private var attachmentDone: TextView? = null
  private var attachmentMultiple = false
  private var attachmentMimeTypes: Array<String> = emptyArray()
  private val selectedImages = LinkedHashSet<Uri>()
  private val recentBadges = mutableMapOf<Uri, TextView>()
  private val imageExecutor = Executors.newFixedThreadPool(2)

  private val filePicker =
    registerForActivityResult(ActivityResultContracts.StartActivityForResult()) { result ->
      dismissAttachmentSheet(deliver = false, animate = false)
      attachmentMimeTypes = emptyArray()
      val callback = pendingFileCallback
      pendingFileCallback = null
      val cameraUri = pendingCameraUri
      val cameraFile = pendingCameraFile
      pendingCameraUri = null
      pendingCameraFile = null

      val selected =
        WebChromeClient.FileChooserParams.parseResult(result.resultCode, result.data)
      val cameraTaken =
        result.resultCode == Activity.RESULT_OK &&
          cameraUri != null &&
          (cameraFile?.length() ?: 0L) > 0L
      Log.i(
        TAG,
        "filePicker resultCode=${result.resultCode} data=${result.data != null} " +
          "cameraLen=${cameraFile?.length() ?: 0L} selected=${selected?.size ?: 0} " +
          "cameraTaken=$cameraTaken",
      )
      val uris =
        when {
          result.resultCode != Activity.RESULT_OK -> null
          cameraTaken -> arrayOf(cameraUri!!)
          selected != null && selected.isNotEmpty() -> selected
          else -> null
        }
      if (!cameraTaken) {
        cameraFile?.delete()
      }
      if (result.resultCode == Activity.RESULT_OK && uris == null) {
        Toast.makeText(this, "没有读取到文件", Toast.LENGTH_SHORT).show()
      }
      Log.i(TAG, "filePicker uris=${uris?.size ?: 0}")
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

  private val imagePermission =
    registerForActivityResult(ActivityResultContracts.RequestMultiplePermissions()) {
      Log.d(TAG, "imagePermission result=$it")
      if (attachmentSheet != null) {
        loadRecentPhotos()
      }
    }

  private val attachmentBackCallback =
    object : OnBackPressedCallback(false) {
      override fun handleOnBackPressed() {
        dismissAttachmentSheet(deliver = true)
      }
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
    onBackPressedDispatcher.addCallback(this, attachmentBackCallback)

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
          Log.i(
            TAG,
            "onShowFileChooser mode=${fileChooserParams?.mode} accept=${accepted.joinToString()}",
          )

          pendingFileCallback?.onReceiveValue(null)
          pendingFileCallback = null
          dismissAttachmentSheet(deliver = false, animate = false)
          clearPendingCamera()
          attachmentMimeTypes = accepted.filter { it.contains("/") }.toTypedArray()
          attachmentMultiple =
            fileChooserParams?.mode == WebChromeClient.FileChooserParams.MODE_OPEN_MULTIPLE
          pendingFileCallback = filePathCallback

          // 图片类附件使用应用内底部面板；ZIP 导入等非图片选择仍直接打开文件选择器。
          val imageExtensions =
            setOf(
              "jpg",
              "jpeg",
              "png",
              "gif",
              "webp",
              "bmp",
              "heic",
              "heif",
              "avif",
              "svg",
              "apng",
              "jfif",
            )
          val acceptsImages =
            accepted.isEmpty() ||
              accepted.any {
                it == "*/*" ||
                  it.startsWith("image/") ||
                  it.substringAfterLast('.', "").lowercase() in imageExtensions
              }

          return try {
            if (acceptsImages) {
              showAttachmentSheet()
            } else {
              launchDocumentPicker(attachmentMimeTypes)
            }
            true
          } catch (e: Exception) {
            Log.e(TAG, "show attachment picker failed", e)
            Toast.makeText(
                this@MainActivity,
                getString(R.string.attachment_open_failed),
                Toast.LENGTH_SHORT,
              )
              .show()
            cancelFileCallback()
            true
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

  private fun showAttachmentSheet() {
    dismissAttachmentSheet(deliver = false, animate = false)
    selectedImages.clear()

    val scrim =
      View(this).apply {
        setBackgroundColor(Color.TRANSPARENT)
        setOnClickListener { dismissAttachmentSheet(deliver = true) }
      }
    root.addView(
      scrim,
      FrameLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.MATCH_PARENT,
      ),
    )

    val sheet =
      LinearLayout(this).apply {
        orientation = LinearLayout.VERTICAL
        setPadding(dp(16), dp(12), dp(16), dp(18))
        background = sheetDrawable(colorSurface(), dp(26))
        elevation = dp(18).toFloat()
        isClickable = true
      }

    val header =
      LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        visibility = View.GONE
        setPadding(dp(4), 0, dp(4), dp(8))
      }
    val headerCount =
      TextView(this).apply {
        textSize = 13f
        setTextColor(colorTextSecondary())
      }
    val done =
      TextView(this).apply {
        text = getString(R.string.attachment_done)
        textSize = 14f
        setTypeface(typeface, android.graphics.Typeface.BOLD)
        setTextColor(colorAccent())
        setPadding(dp(12), dp(6), dp(4), dp(6))
        isClickable = true
        isFocusable = true
        setOnClickListener {
          if (selectedImages.isNotEmpty()) {
            deliverUris(selectedImages.toTypedArray())
          }
        }
      }
    header.addView(
      headerCount,
      LinearLayout.LayoutParams(0, ViewGroup.LayoutParams.WRAP_CONTENT, 1f),
    )
    header.addView(done)
    sheet.addView(header)

    val recentSection = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL }
    val recentRow =
      LinearLayout(this).apply {
        orientation = LinearLayout.HORIZONTAL
        gravity = Gravity.CENTER_VERTICAL
        minimumHeight = dp(86)
      }
    val recentScroll =
      HorizontalScrollView(this).apply {
        isHorizontalScrollBarEnabled = false
        overScrollMode = View.OVER_SCROLL_NEVER
        addView(recentRow)
      }
    val recentHint =
      TextView(this).apply {
        textSize = 12f
        setTextColor(colorAccent())
        setPadding(dp(4), dp(10), dp(4), dp(10))
        visibility = View.GONE
        setOnClickListener {
          runCatching {
            startActivity(
              Intent(
                Settings.ACTION_APPLICATION_DETAILS_SETTINGS,
                Uri.fromParts("package", packageName, null),
              )
            )
          }
        }
      }
    recentSection.addView(
      recentScroll,
      LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT,
      ),
    )
    recentSection.addView(
      recentHint,
      LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT,
      ),
    )
    sheet.addView(
      recentSection,
      LinearLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT,
      )
        .apply { bottomMargin = dp(16) },
    )

    val actions = LinearLayout(this).apply { orientation = LinearLayout.HORIZONTAL }
    val cameraButton =
      attachmentAction(R.drawable.ic_camera, R.string.attachment_take_photo) { launchCamera() }
    val galleryButton =
      attachmentAction(R.drawable.ic_gallery, R.string.attachment_gallery) { launchGalleryPicker() }
    val filesButton =
      attachmentAction(R.drawable.ic_file, R.string.attachment_files) { launchFilesPicker() }
    actions.addView(
      cameraButton,
      LinearLayout.LayoutParams(0, dp(96), 1f).apply { marginEnd = dp(10) },
    )
    actions.addView(
      galleryButton,
      LinearLayout.LayoutParams(0, dp(96), 1f).apply { marginEnd = dp(10) },
    )
    actions.addView(filesButton, LinearLayout.LayoutParams(0, dp(96), 1f))
    sheet.addView(actions)

    root.addView(
      sheet,
      FrameLayout.LayoutParams(
        ViewGroup.LayoutParams.MATCH_PARENT,
        ViewGroup.LayoutParams.WRAP_CONTENT,
        Gravity.BOTTOM,
      ),
    )

    attachmentScrim = scrim
    attachmentSheet = sheet
    attachmentHeader = header
    attachmentHeaderCount = headerCount
    attachmentDone = done
    attachmentRecentSection = recentSection
    attachmentRecentRow = recentRow
    attachmentRecentHint = recentHint
    attachmentBackCallback.isEnabled = true

    WindowCompat.getInsetsController(window, root).hide(WindowInsetsCompat.Type.ime())
    sheet.translationY = dp(380).toFloat()
    sheet.animate().translationY(0f).setDuration(220).start()

    updateSelectionUi()
    ensureImageAccess()
  }

  private fun attachmentAction(
    iconRes: Int,
    labelRes: Int,
    onClick: () -> Unit,
  ): LinearLayout =
    LinearLayout(this).apply {
      orientation = LinearLayout.VERTICAL
      gravity = Gravity.CENTER
      background = rippleDrawable(colorCard(), dp(20))
      isClickable = true
      isFocusable = true
      contentDescription = getString(labelRes)
      setOnClickListener { onClick() }
      addView(
        ImageView(this@MainActivity).apply {
          setImageResource(iconRes)
          imageTintList = ColorStateList.valueOf(colorTextPrimary())
          layoutParams = LinearLayout.LayoutParams(dp(28), dp(28))
        }
      )
      addView(
        TextView(this@MainActivity).apply {
          text = getString(labelRes)
          textSize = 15f
          setTextColor(colorTextPrimary())
          layoutParams =
            LinearLayout.LayoutParams(
              ViewGroup.LayoutParams.WRAP_CONTENT,
              ViewGroup.LayoutParams.WRAP_CONTENT,
            )
              .apply { topMargin = dp(10) }
        }
      )
    }

  private fun updateSelectionUi() {
    val count = selectedImages.size
    attachmentHeader?.visibility = if (count > 0) View.VISIBLE else View.GONE
    attachmentHeaderCount?.text = getString(R.string.attachment_selected_count, count)
    attachmentDone?.text =
      if (count > 0) {
        "${getString(R.string.attachment_done)} ($count)"
      } else {
        getString(R.string.attachment_done)
      }
    recentBadges.forEach { (uri, badge) ->
      val index = selectedImages.indexOf(uri)
      val selected = index >= 0
      badge.text = if (selected) (index + 1).toString() else ""
      badge.background = selectionBadgeDrawable(selected)
    }
  }

  private fun renderRecentPhotos(items: List<Pair<Uri, Bitmap>>) {
    val row = attachmentRecentRow ?: return
    val section = attachmentRecentSection ?: return
    row.removeAllViews()
    recentBadges.clear()
    selectedImages.clear()
    updateSelectionUi()
    if (items.isEmpty()) {
      section.visibility = View.GONE
      return
    }
    section.visibility = View.VISIBLE
    items.forEach { (uri, bitmap) ->
      val thumb =
        FrameLayout(this).apply {
          layoutParams =
            LinearLayout.LayoutParams(dp(86), dp(86)).apply { marginEnd = dp(8) }
          background = roundedDrawable(colorCard(), dp(14))
          clipToOutline = true
          isClickable = true
          isFocusable = true
          contentDescription = getString(R.string.attachment_recent)
        }
      thumb.addView(
        ImageView(this).apply {
          setImageBitmap(bitmap)
          scaleType = ImageView.ScaleType.CENTER_CROP
          layoutParams =
            FrameLayout.LayoutParams(
              ViewGroup.LayoutParams.MATCH_PARENT,
              ViewGroup.LayoutParams.MATCH_PARENT,
            )
        }
      )
      val badge =
        TextView(this).apply {
          gravity = Gravity.CENTER
          textSize = 12f
          setTextColor(Color.WHITE)
          background = selectionBadgeDrawable(false)
          layoutParams =
            FrameLayout.LayoutParams(dp(24), dp(24), Gravity.TOP or Gravity.END).apply {
              topMargin = dp(6)
              marginEnd = dp(6)
            }
          visibility = if (attachmentMultiple) View.VISIBLE else View.GONE
        }
      thumb.addView(badge)
      thumb.setOnClickListener {
        if (attachmentMultiple) {
          if (!selectedImages.remove(uri)) {
            selectedImages.add(uri)
          }
          updateSelectionUi()
        } else {
          deliverUris(arrayOf(uri))
        }
      }
      row.addView(thumb)
      recentBadges[uri] = badge
    }
    updateSelectionUi()
  }

  private fun loadRecentPhotos() {
    val row = attachmentRecentRow ?: return
    val hint = attachmentRecentHint ?: return
    if (!hasImagePermission()) {
      showRecentPermissionHint()
      return
    }
    hint.visibility = View.GONE
    row.visibility = View.VISIBLE
    row.removeAllViews()
    recentBadges.clear()
    row.addView(
      ProgressBar(this).apply {
        layoutParams =
          LinearLayout.LayoutParams(dp(28), dp(28)).apply {
            gravity = Gravity.CENTER_VERTICAL
            marginEnd = dp(8)
          }
      }
    )
    val thumbSize = Size(dp(86), dp(86))
    imageExecutor.execute {
      val photos = queryRecentImageUris()
      val thumbs = mutableListOf<Pair<Uri, Bitmap>>()
      for (uri in photos) {
        val bitmap =
          runCatching { contentResolver.loadThumbnail(uri, thumbSize, null) }.getOrNull()
        if (bitmap != null) {
          thumbs.add(uri to bitmap)
        }
      }
      mainHandler.post {
        if (attachmentRecentRow !== row || attachmentSheet == null) {
          thumbs.forEach { it.second.recycle() }
          return@post
        }
        renderRecentPhotos(thumbs)
      }
    }
  }

  private fun queryRecentImageUris(): List<Uri> {
    val collection = MediaStore.Images.Media.EXTERNAL_CONTENT_URI
    val projection = arrayOf(MediaStore.Images.Media._ID)
    val args =
      Bundle().apply {
        putStringArray(
          ContentResolver.QUERY_ARG_SORT_COLUMNS,
          arrayOf(MediaStore.Images.Media.DATE_ADDED),
        )
        putInt(
          ContentResolver.QUERY_ARG_SORT_DIRECTION,
          ContentResolver.QUERY_SORT_DIRECTION_DESCENDING,
        )
        putInt(ContentResolver.QUERY_ARG_LIMIT, 20)
      }
    val cursor =
      runCatching { contentResolver.query(collection, projection, args, null) }.getOrNull()
        ?: runCatching {
          contentResolver.query(
            collection,
            projection,
            null,
            null,
            "${MediaStore.Images.Media.DATE_ADDED} DESC",
          )
        }
          .getOrNull()
    val result = mutableListOf<Uri>()
    cursor?.use {
      val idColumn = it.getColumnIndexOrThrow(MediaStore.Images.Media._ID)
      while (it.moveToNext() && result.size < 20) {
        result.add(ContentUris.withAppendedId(collection, it.getLong(idColumn)))
      }
    }
    return result
  }

  private fun ensureImageAccess() {
    if (hasImagePermission()) {
      loadRecentPhotos()
      return
    }
    val permissions =
      when {
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE ->
          arrayOf(
            Manifest.permission.READ_MEDIA_IMAGES,
            Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED,
          )
        Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ->
          arrayOf(Manifest.permission.READ_MEDIA_IMAGES)
        else -> arrayOf(Manifest.permission.READ_EXTERNAL_STORAGE)
      }
    try {
      imagePermission.launch(permissions)
    } catch (e: Exception) {
      Log.w(TAG, "image permission request failed", e)
      showRecentPermissionHint()
    }
  }

  private fun hasImagePermission(): Boolean =
    when {
      Build.VERSION.SDK_INT >= Build.VERSION_CODES.UPSIDE_DOWN_CAKE ->
        ContextCompat.checkSelfPermission(this, Manifest.permission.READ_MEDIA_IMAGES) ==
          PackageManager.PERMISSION_GRANTED ||
          ContextCompat.checkSelfPermission(
            this,
            Manifest.permission.READ_MEDIA_VISUAL_USER_SELECTED,
          ) == PackageManager.PERMISSION_GRANTED
      Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU ->
        ContextCompat.checkSelfPermission(this, Manifest.permission.READ_MEDIA_IMAGES) ==
          PackageManager.PERMISSION_GRANTED
      else ->
        ContextCompat.checkSelfPermission(this, Manifest.permission.READ_EXTERNAL_STORAGE) ==
          PackageManager.PERMISSION_GRANTED
    }

  private fun showRecentPermissionHint() {
    attachmentRecentRow?.visibility = View.GONE
    attachmentRecentHint?.apply {
      text = getString(R.string.attachment_photo_permission_denied)
      visibility = View.VISIBLE
    }
  }

  private fun launchCamera() {
    val photo =
      try {
        val cameraDir = File(cacheDir, "camera").apply { mkdirs() }
        File.createTempFile("photo-", ".jpg", cameraDir)
      } catch (e: Exception) {
        Log.e(TAG, "create camera file failed", e)
        Toast.makeText(this, getString(R.string.attachment_camera_unavailable), Toast.LENGTH_SHORT)
          .show()
        return
      }
    val photoUri =
      try {
        FileProvider.getUriForFile(this, "${packageName}.fileprovider", photo)
      } catch (e: Exception) {
        Log.e(TAG, "camera FileProvider uri failed", e)
        photo.delete()
        Toast.makeText(this, getString(R.string.attachment_camera_unavailable), Toast.LENGTH_SHORT)
          .show()
        return
      }
    val intent =
      Intent(MediaStore.ACTION_IMAGE_CAPTURE).apply {
        putExtra(MediaStore.EXTRA_OUTPUT, photoUri)
        addFlags(
          Intent.FLAG_GRANT_WRITE_URI_PERMISSION or
            Intent.FLAG_GRANT_READ_URI_PERMISSION
        )
        clipData = ClipData.newRawUri("camera-output", photoUri)
      }
    val resolved = runCatching { intent.resolveActivity(packageManager) }.getOrNull()
    Log.i(TAG, "camera resolved=${resolved?.packageName} uri=$photoUri")
    if (resolved == null) {
      photo.delete()
      Toast.makeText(this, getString(R.string.attachment_camera_unavailable), Toast.LENGTH_SHORT)
        .show()
      return
    }
    val grantFlags =
      Intent.FLAG_GRANT_WRITE_URI_PERMISSION or Intent.FLAG_GRANT_READ_URI_PERMISSION
    runCatching { grantUriPermission(resolved.packageName, photoUri, grantFlags) }
    runCatching {
      packageManager.queryIntentActivities(intent, 0).forEach { info ->
        runCatching { grantUriPermission(info.activityInfo.packageName, photoUri, grantFlags) }
      }
    }
    pendingCameraUri = photoUri
    pendingCameraFile = photo
    dismissAttachmentSheet(deliver = false, animate = false)
    launchPicker(intent, getString(R.string.attachment_camera_unavailable))
  }

  private fun launchGalleryPicker() {
    val intent =
      if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
        Intent(MediaStore.ACTION_PICK_IMAGES).apply {
          type = "image/*"
          if (attachmentMultiple) {
            putExtra(Intent.EXTRA_ALLOW_MULTIPLE, true)
          }
        }
      } else {
        Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
          addCategory(Intent.CATEGORY_OPENABLE)
          type = "image/*"
          putExtra(Intent.EXTRA_ALLOW_MULTIPLE, attachmentMultiple)
        }
      }
    if (runCatching { intent.resolveActivity(packageManager) }.getOrNull() == null) {
      dismissAttachmentSheet(deliver = false, animate = false)
      launchDocumentPicker(arrayOf("image/*"))
      return
    }
    dismissAttachmentSheet(deliver = false, animate = false)
    launchPicker(intent, getString(R.string.attachment_gallery))
  }

  private fun launchFilesPicker() {
    dismissAttachmentSheet(deliver = false, animate = false)
    launchDocumentPicker(attachmentMimeTypes)
  }

  private fun launchDocumentPicker(mimeTypes: Array<String>) {
    dismissAttachmentSheet(deliver = false, animate = false)
    val intent =
      Intent(Intent.ACTION_OPEN_DOCUMENT).apply {
        addCategory(Intent.CATEGORY_OPENABLE)
        type = if (mimeTypes.size == 1) mimeTypes[0] else "*/*"
        if (mimeTypes.isNotEmpty()) {
          putExtra(Intent.EXTRA_MIME_TYPES, mimeTypes)
        }
        putExtra(Intent.EXTRA_ALLOW_MULTIPLE, attachmentMultiple)
      }
    launchPicker(intent, getString(R.string.attachment_open_failed))
  }

  private fun launchPicker(intent: Intent, failureMessage: String) {
    try {
      filePicker.launch(intent)
    } catch (e: Exception) {
      Log.e(TAG, "picker launch failed", e)
      Toast.makeText(this, failureMessage, Toast.LENGTH_SHORT).show()
      cancelFileCallback()
    }
  }

  private fun deliverUris(uris: Array<Uri>) {
    val callback = pendingFileCallback
    pendingFileCallback = null
    attachmentMimeTypes = emptyArray()
    dismissAttachmentSheet(deliver = false, animate = false)
    Log.i(TAG, "filePicker deliver uris=${uris.size}")
    callback?.onReceiveValue(uris)
  }

  private fun cancelFileCallback() {
    val callback = pendingFileCallback
    pendingFileCallback = null
    attachmentMimeTypes = emptyArray()
    dismissAttachmentSheet(deliver = false, animate = false)
    clearPendingCamera()
    Log.i(TAG, "filePicker cancel")
    callback?.onReceiveValue(null)
  }

  private fun clearPendingCamera() {
    pendingCameraFile?.delete()
    pendingCameraFile = null
    pendingCameraUri = null
  }

  private fun dismissAttachmentSheet(deliver: Boolean, animate: Boolean = true) {
    val sheet = attachmentSheet
    val scrim = attachmentScrim
    attachmentSheet = null
    attachmentScrim = null
    attachmentRecentSection = null
    attachmentRecentRow = null
    attachmentRecentHint = null
    attachmentHeader = null
    attachmentHeaderCount = null
    attachmentDone = null
    recentBadges.clear()
    selectedImages.clear()
    attachmentBackCallback.isEnabled = false

    if (sheet != null) {
      sheet.animate().cancel()
      if (animate) {
        val distance = maxOf(sheet.height, dp(360)).toFloat()
        sheet
          .animate()
          .translationY(distance)
          .setDuration(180)
          .withEndAction { root.removeView(sheet) }
          .start()
      } else {
        root.removeView(sheet)
      }
    }
    scrim?.let { root.removeView(it) }

    if (deliver) {
      val callback = pendingFileCallback
      pendingFileCallback = null
      attachmentMimeTypes = emptyArray()
      clearPendingCamera()
      Log.i(TAG, "filePicker cancelled")
      callback?.onReceiveValue(null)
    }
  }

  private fun colorSurface(): Int =
    if (isNight()) Color.rgb(28, 30, 34) else Color.rgb(247, 248, 250)

  private fun colorCard(): Int =
    if (isNight()) Color.rgb(42, 45, 50) else Color.rgb(238, 240, 244)

  private fun colorTextPrimary(): Int =
    if (isNight()) Color.rgb(236, 237, 239) else Color.rgb(26, 28, 32)

  private fun colorTextSecondary(): Int =
    if (isNight()) Color.rgb(158, 162, 168) else Color.rgb(122, 126, 132)

  private fun colorAccent(): Int =
    if (isNight()) Color.rgb(130, 152, 255) else Color.rgb(77, 107, 254)

  private fun sheetDrawable(color: Int, radius: Int): GradientDrawable =
    GradientDrawable().apply {
      shape = GradientDrawable.RECTANGLE
      setColor(color)
      cornerRadii =
        floatArrayOf(
          radius.toFloat(),
          radius.toFloat(),
          radius.toFloat(),
          radius.toFloat(),
          0f,
          0f,
          0f,
          0f,
        )
    }

  private fun roundedDrawable(color: Int, radius: Int): GradientDrawable =
    GradientDrawable().apply {
      shape = GradientDrawable.RECTANGLE
      setColor(color)
      cornerRadius = radius.toFloat()
    }

  private fun rippleDrawable(fill: Int, radius: Int): RippleDrawable =
    RippleDrawable(
      ColorStateList.valueOf(
        if (isNight()) Color.argb(40, 255, 255, 255) else Color.argb(30, 0, 0, 0)
      ),
      roundedDrawable(fill, radius),
      roundedDrawable(Color.WHITE, radius),
    )

  private fun selectionBadgeDrawable(selected: Boolean): GradientDrawable =
    GradientDrawable().apply {
      shape = GradientDrawable.OVAL
      if (selected) {
        setColor(colorAccent())
      } else {
        setColor(Color.argb(32, 0, 0, 0))
        setStroke(dp(2), Color.WHITE)
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
    if (attachmentSheet != null && attachmentRecentRow?.childCount == 0 && hasImagePermission()) {
      loadRecentPhotos()
    }
  }

  override fun onDestroy() {
    dismissAttachmentSheet(deliver = true, animate = false)
    destroyWebView()
    executor.shutdownNow()
    imageExecutor.shutdownNow()
    super.onDestroy()
  }
}
