import { ApiError, deleteFile, request, uploadFile } from '../api/client.js'
import type { FilePurpose } from '../api/types.js'
import { createId } from '../../utils/id.js'
import { toSessionSummary, type Session, type SessionSummary } from './types.js'

/** 文件原件由服务端保存；这个类型保留给历史导入/导出代码做参数标注。 */
export type StoredFile = {
  id: string
  blob: Blob
}

const API = '/api/v1'

/** 每个会话的当前 version 只在内存中维护；刷新后由 GET 会话重新填充。 */
const versions = new Map<string, number>()
/** 同一会话的保存串行执行，避免连续 persist 用同一个 version 互相冲突。 */
const queues = new Map<string, Promise<unknown>>()

const enqueue = <T>(id: string, task: () => Promise<T>): Promise<T> => {
  const previous = queues.get(id) ?? Promise.resolve()
  const next = previous.catch(() => undefined).then(task)
  queues.set(
    id,
    next
      .then(
        () => undefined,
        () => undefined
      )
      .finally(() => {
        if (queues.get(id) === next) {
          queues.delete(id)
        }
      })
  )
  return next
}

const readJSON = async <T>(response: Response): Promise<T> => (await response.json()) as T

const fetchChat = async (
  id: string
): Promise<{ session: Session; version: number } | undefined> => {
  try {
    const response = await request(`${API}/chats/${encodeURIComponent(id)}`, { method: 'GET' })
    const body = await readJSON<{ session: Session; version: number }>(response)
    versions.set(id, body.version)
    return body
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      return undefined
    }
    throw error
  }
}

const createChat = async (session: Session): Promise<number> => {
  const response = await request(`${API}/chats`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session }),
  })
  const body = await readJSON<{ version: number }>(response)
  versions.set(session.id, body.version)
  return body.version
}

const updateChat = async (session: Session, version: number): Promise<number> => {
  const response = await request(`${API}/chats/${encodeURIComponent(session.id)}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ session, version }),
  })
  const body = await readJSON<{ version: number }>(response)
  versions.set(session.id, body.version)
  return body.version
}

const putChat = async (session: Session): Promise<void> => {
  const version = versions.get(session.id)
  if (version === undefined) {
    try {
      await createChat(session)
      return
    } catch (error) {
      // 上一次创建可能已经成功但响应丢失；重新读取后按更新处理。
      if (!(error instanceof ApiError) || error.status !== 409) {
        throw error
      }
      const existing = await fetchChat(session.id)
      if (!existing) {
        throw error
      }
    }
  }
  await updateChat(session, versions.get(session.id) ?? 1)
}

export const loadSessionSummaries = async (): Promise<SessionSummary[]> => {
  const response = await request(`${API}/chats`, { method: 'GET' })
  const body = await readJSON<{ chats: SessionSummary[] }>(response)
  return body.chats.map(toSessionSummary)
}

export const loadSession = async (id: string): Promise<Session | undefined> => {
  const stored = await fetchChat(id)
  return stored?.session
}

export const saveSession = async (session: Session, files: StoredFile[] = []): Promise<void> => {
  void files
  await enqueue(session.id, () => putChat(session))
}

/** 未打开的会话也可重命名：先读最新 version，再整体 PUT。 */
export const renameSession = async (id: string, title: string): Promise<void> => {
  await enqueue(id, async () => {
    const stored = await fetchChat(id)
    if (!stored) {
      return
    }
    await updateChat({ ...stored.session, title, updatedAt: Date.now() }, stored.version)
  })
}

export const deleteSession = async (sessionId: string): Promise<void> => {
  await enqueue(sessionId, async () => {
    try {
      // 会话删除是永久的；服务端会同时清理该会话独占的附件。
      await request(`${API}/chats/${encodeURIComponent(sessionId)}`, { method: 'DELETE' })
    } catch (error) {
      if (!(error instanceof ApiError) || error.status !== 404) {
        throw error
      }
    }
    versions.delete(sessionId)
  })
}

/** 附件已在上传时写入服务端，保存会话不再需要搬 Blob。 */
export const saveFiles = async (files: StoredFile[]): Promise<void> => {
  void files
}

export const loadFile = async (id: string): Promise<Blob | undefined> => {
  try {
    const response = await request(`${API}/files/${encodeURIComponent(id)}`, { method: 'GET' })
    return await response.blob()
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      return undefined
    }
    throw error
  }
}

// 历史导入在浏览器内存里暂存；全部写完后再调用 commitStaging。
let stagedSessions = new Map<string, Session>()
let stagedFiles = new Map<string, StoredFile>()

export const clearStaging = async (): Promise<void> => {
  stagedSessions = new Map()
  stagedFiles = new Map()
}

export const addStagingSession = async (session: Session): Promise<void> => {
  stagedSessions.set(session.id, session)
}

export const saveStagingFiles = async (files: StoredFile[]): Promise<void> => {
  for (const file of files) {
    stagedFiles.set(file.id, file)
  }
}

export const updateStagingFileTypes = async (types: ReadonlyMap<string, string>): Promise<void> => {
  for (const session of stagedSessions.values()) {
    for (const message of session.messages) {
      if (message.role !== 'user') {
        continue
      }
      for (const file of message.files) {
        const type = types.get(file.id)
        if (type !== undefined) {
          file.mimeType = type
        }
      }
    }
  }
}

const purposeFor = (name: string, isImage: boolean): FilePurpose | null => {
  if (isImage) {
    return null
  }
  return /\.(pdf|doc|docx|ppt|pptx)$/i.test(name) ? 'rich_layout_recognition' : 'user_data'
}

const allChatIDs = async (): Promise<string[]> => {
  const response = await request(`${API}/chats`, { method: 'GET' })
  const body = await readJSON<{ chats: SessionSummary[] }>(response)
  return body.chats.map((chat) => chat.id)
}

/** 清空当前账户的全部会话与附件；先逐个永久删除，再清掉草稿等未引用文件。 */
const clearAllChats = async () => {
  for (const id of await allChatIDs()) {
    await deleteSession(id)
  }
  await request(`${API}/files`, { method: 'DELETE' })
}

/**
 * 用暂存原子替换正式数据：先给导入附件换新 ID 并上传，再让服务端在一个事务里替换全部会话。
 * 上传或导入失败时只清理新上传的文件，旧会话和旧附件保持不变。
 */
export const commitStaging = async (): Promise<SessionSummary[]> => {
  if (stagedSessions.size === 0) {
    await clearAllChats()
    return []
  }
  remapStagedIDs()
  const metadata = new Map<string, { name: string; isImage: boolean }>()
  for (const session of stagedSessions.values()) {
    for (const message of session.messages) {
      if (message.role === 'user') {
        for (const file of message.files) {
          metadata.set(file.id, { name: file.name, isImage: file.isImage })
        }
      }
    }
  }
  const uploaded: string[] = []
  try {
    for (const [id, stored] of stagedFiles) {
      const info = metadata.get(id) ?? { name: id, isImage: false }
      uploaded.push(id)
      await uploadFile(
        new File([stored.blob], info.name, { type: stored.blob.type }),
        purposeFor(info.name, info.isImage),
        info.isImage,
        id
      )
    }
    await request(`${API}/import`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ sessions: [...stagedSessions.values()] }),
    })
  } catch (error) {
    await Promise.all(uploaded.map((id) => deleteFile(id).catch(() => undefined)))
    throw error
  }
  const summaries: SessionSummary[] = []
  for (const session of stagedSessions.values()) {
    // 服务端导入后版本固定从 1 开始，直接记录可避免第一次保存先撞 409。
    versions.set(session.id, 1)
    summaries.push(toSessionSummary(session))
  }
  return summaries
}

/**
 * 导入文件使用新 ID，避免覆盖服务端已有附件；失败回滚时旧文件仍可用。
 * 没有原件的附件也换 ID，避免新会话误引用旧文件。
 */
const remapStagedIDs = () => {
  const ids = new Map<string, string>()
  for (const id of stagedFiles.keys()) {
    ids.set(id, createId())
  }
  const remap = (id: string) => {
    let next = ids.get(id)
    if (!next) {
      next = createId()
      ids.set(id, next)
    }
    return next
  }
  for (const session of stagedSessions.values()) {
    for (const message of session.messages) {
      if (message.role !== 'user') {
        continue
      }
      for (const file of message.files) {
        file.id = remap(file.id)
        file.serverId = null
        file.imageSizeChecked = false
      }
    }
  }
  stagedFiles = new Map([...stagedFiles].map(([id, stored]) => [remap(id), stored] as const))
}

/** 服务端错误无法像 IndexedDB 那样在本地兜底，保留统一日志出口方便组件复用。 */
export const reportStorageError = (action: string) => (error: unknown) => {
  // eslint-disable-next-line no-console
  console.error(`[server] ${action} failed`, error)
}
