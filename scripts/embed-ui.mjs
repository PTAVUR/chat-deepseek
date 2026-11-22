import { cp, mkdir, rm, writeFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const root = path.dirname(path.dirname(fileURLToPath(import.meta.url)))
const source = path.join(root, 'dist')
const target = path.join(root, 'internal', 'webui', 'dist')

await rm(target, { recursive: true, force: true })
await mkdir(target, { recursive: true })
await cp(source, target, { recursive: true })
// 保留占位文件，确保未构建时 go:embed 也能编译。
await writeFile(path.join(target, '.gitkeep'), '')
console.log(`Copied ${source} -> ${target}`)
