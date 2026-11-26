# chat-deepseek Android

自包含 Android 客户端：手机本地运行 Go 服务端，WebView 打开 `127.0.0.1` 上的聊天界面。不需要另外部署服务器，聊天时仍然需要联网调用 DeepSeek API。

## 组成

```
android/
  app/src/main/
    kotlin/.../ChatApp.kt          Application，持有 ServerManager
    kotlin/.../ServerManager.kt    setup / serve 子进程、端口、日志
    kotlin/.../MainActivity.kt     首次创建 Owner、WebView、权限、附件选择、图片预览
    kotlin/.../ServerService.kt     前台服务，保活本地服务并显示通知
    res/                           图标、通知文案
  dist/                            构建产物（APK + SHA256SUMS.txt）
```

Go 服务端以 `jniLibs/arm64-v8a/libchatdeepseek.so` 形式打包。Android 10+ 只允许从只读的 `nativeLibraryDir` 执行，因此必须保留 `useLegacyPackaging = true`，并且不要在构建后重命名这个文件。

## 环境

已在本机验证：

- Go 1.27.4
- Node.js 22.12+、pnpm
- JDK 17+（Android Studio 自带 JBR 可用）
- Android SDK：Platform 37、Build-Tools 37.0.0、Platform-Tools
- 设置 `JAVA_HOME`、`ANDROID_HOME`（或 `ANDROID_SDK_ROOT`）

Android Studio 不是构建必需的；Gradle wrapper 足够。

## 构建 APK

在仓库根目录：

```powershell
pnpm install
node scripts/build-android.mjs
```

脚本会依次：

1. `rspack` 构建生产前端并复制到 `internal/webui/dist`
2. 交叉编译 `GOOS=android GOARCH=arm64 CGO_ENABLED=0` 的服务端，写入 `jniLibs`
3. 执行 `android/gradlew assembleRelease`
4. 复制 APK 到 `android/dist/` 并生成 `SHA256SUMS.txt`

输出：

```
android/dist/chat-deepseek-<version>.apk
android/dist/SHA256SUMS.txt
```

## 签名

`android/keystore.properties` 不提交到 Git，格式见 `android/keystore.properties.example`。

升级安装必须使用同一个 keystore，否则 Android 会拒绝覆盖安装，只能卸载重装（会丢数据）。请把 keystore 和 `keystore.properties` 单独备份。

## 首次使用

1. 安装 APK。
2. 打开后创建 Owner（用户名默认 `owner`，密码至少 8 位；没有找回功能）。
3. 登录 Web UI，在「系统设置 → 数据管理」填写 DeepSeek API Key。
4. 打开前台服务通知，可让本地服务在后台保持运行；通知里的「停止服务」会同时停止本地服务。

## 已知限制

- 仅打包 `arm64-v8a`。
- 前台服务使用 `specialUse` 类型，满足 Android 14+ 的声明要求；通知权限由首次启动时申请。
- WebView 导出 blob 文件仍受 Android WebView 限制；附件上传、语音输入、图片预览、TTS 已接入。
- 数据保存在应用私有目录 `files/data`；卸载应用会删除数据。不要把 `master.key` 或 `keystore.properties` 提交到仓库。
- 当前未提供内置整库备份入口；后续可补原生导出 ZIP。
