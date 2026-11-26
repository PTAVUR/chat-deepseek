import { spawnSync } from 'node:child_process'
import { createHash } from 'node:crypto'
import {
  copyFileSync,
  existsSync,
  mkdirSync,
  readFileSync,
  rmSync,
  writeFileSync,
} from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const androidDir = path.join(root, 'android')
const isWindows = process.platform === 'win32'

const run = (command, args, { cwd = root, env = {}, shell = isWindows } = {}) => {
  console.log(`\n$ ${command} ${args.join(' ')}`)
  const result = spawnSync(command, args, {
    cwd,
    env: { ...process.env, ...env },
    stdio: 'inherit',
    // Windows 的 pnpm / gradlew 是 .cmd/.bat，需要 shell 才能正确解析。
    shell,
  })
  if (result.error) {
    throw result.error
  }
  if (result.status !== 0) {
    process.exit(result.status ?? 1)
  }
}

const go = process.env.GO ?? 'go'
const pnpm = process.env.PNPM ?? 'pnpm'

// 1. Web UI：production 构建后嵌入 Go 的 internal/webui/dist。
rmSync(path.join(root, 'dist'), { recursive: true, force: true })
run(pnpm, ['exec', 'rspack', 'build', '-c', 'rspack.config.ts'], {
  env: { NODE_ENV: 'production' },
})
run(process.execPath, [path.join(root, 'scripts', 'embed-ui.mjs')], { shell: false })

// 2. Go 服务端：Android arm64 PIE，放入 jniLibs，安装时由系统解压到 nativeLibraryDir。
const jniDir = path.join(androidDir, 'app', 'src', 'main', 'jniLibs', 'arm64-v8a')
const serverBinary = path.join(jniDir, 'libchatdeepseek.so')
mkdirSync(jniDir, { recursive: true })
run(go, [
  'build',
  '-trimpath',
  '-buildmode=pie',
  '-ldflags',
  '-s -w',
  '-o',
  serverBinary,
  './cmd/chat-deepseek',
], {
  shell: false,
  env: {
    GOOS: 'android',
    GOARCH: 'arm64',
    CGO_ENABLED: '0',
    GOTOOLCHAIN: process.env.GOTOOLCHAIN ?? 'local',
  },
})

// 3. Android release APK。签名读取 android/keystore.properties。
if (!existsSync(path.join(androidDir, 'keystore.properties'))) {
  console.warn(
    '\nWARNING: android/keystore.properties 不存在，release 包将不会使用正式签名。'
  )
}
run(isWindows ? path.join(androidDir, 'gradlew.bat') : path.join(androidDir, 'gradlew'), [
  '--no-daemon',
  'assembleRelease',
], { cwd: androidDir })

// 4. 复制 APK 和校验和到 android/dist。
const buildGradle = readFileSync(path.join(androidDir, 'app', 'build.gradle.kts'), 'utf8')
const version = buildGradle.match(/versionName\s*=\s*"([^"]+)"/)?.[1] ?? 'android'
const apk = path.join(
  androidDir,
  'app',
  'build',
  'outputs',
  'apk',
  'release',
  'app-release.apk'
)
if (!existsSync(apk)) {
  throw new Error(`APK not found: ${apk}`)
}
const distDir = path.join(androidDir, 'dist')
rmSync(distDir, { recursive: true, force: true })
mkdirSync(distDir, { recursive: true })
const output = path.join(distDir, `chat-deepseek-${version}.apk`)
copyFileSync(apk, output)
const hash = createHash('sha256').update(readFileSync(output)).digest('hex')
writeFileSync(
  path.join(distDir, 'SHA256SUMS.txt'),
  `${hash}  ${path.basename(output)}\n`,
  'utf8'
)
console.log(`\nAPK: ${output}\nSHA-256: ${hash}`)
