import type { TranslationMessage } from '../../i18n/types.js'
import { resizeUploadImage } from '../../utils/resizeUploadImage.js'
import { readMessageEvent } from './parseEvent.js'
import { readServerSentEvents } from './sse.js'
import type {
  ApiErrorBody,
  CreateMessageRequest,
  FileObject,
  FilePurpose,
  MessageStreamEvent,
} from './types.js'

export type ApiErrorKind =
  /** 非 2xx 响应 */
  | 'http'
  /** 流中返回的错误帧（如等待生成超时），随后连接关闭 */
  | 'stream'
  /** 请求没有到达服务端（断网、跨域被拦截等） */
  | 'network'
  /** 流在 message_stop 之前结束 */
  | 'interrupted'
  /** 响应不符合协议（如非 SSE 响应） */
  | 'protocol'

export class ApiError extends Error {
  readonly localMessage?: TranslationMessage

  constructor(
    readonly kind: ApiErrorKind,
    message: string | TranslationMessage,
    readonly status?: number
  ) {
    super(typeof message === 'string' ? message : message.key)
    this.name = 'ApiError'
    this.localMessage = typeof message === 'string' ? undefined : message
  }
}

const isErrorBody = (value: unknown): value is ApiErrorBody =>
  typeof value === 'object' &&
  value !== null &&
  'error' in value &&
  typeof (value as ApiErrorBody).error?.message === 'string'

const readErrorResponse = async (
  response: Response,
  kind: 'http' | 'protocol' = 'http',
  fallback = response.statusText
): Promise<ApiError> => {
  const text = await response.text().catch(() => '')
  let message = text || fallback
  try {
    const body: unknown = JSON.parse(text)
    if (isErrorBody(body)) {
      message = body.error.message
    }
  } catch {
    // 非 JSON 响应体直接展示原文
  }
  return new ApiError(kind, message, response.status)
}

/** CSRF token 只保存在内存里，刷新页面后由 /auth/me 重新下发。 */
let csrfToken = ''
let unauthorizedHandler: (() => void) | null = null

export const setCSRFToken = (token: string) => {
  csrfToken = token
}

/** 会话失效时通知界面回到登录页；DeepSeek 自己的 401 不会触发。 */
export const onUnauthorized = (handler: () => void) => {
  unauthorizedHandler = handler
  return () => {
    if (unauthorizedHandler === handler) {
      unauthorizedHandler = null
    }
  }
}

const MUTATING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE'])

/** 登录、改密、校验 Key 的 401 是业务错误，不应把用户踢回登录页。 */
const isAuthAttempt = (url: string) =>
  url.includes('/auth/login') ||
  url.includes('/auth/me') ||
  url.includes('/auth/password') ||
  url.includes('/settings/api-key/verify')

type RequestOptions = {
  /** 默认 true；DeepSeek 透传的 401 要设为 false，避免误判会话过期 */
  handleUnauthorized?: boolean
}

export const request = async (
  url: string,
  init: RequestInit,
  options: RequestOptions = {}
): Promise<Response> => {
  const method = (init.method ?? 'GET').toUpperCase()
  const headers = new Headers(init.headers)
  if (MUTATING_METHODS.has(method) && csrfToken) {
    headers.set('X-CSRF-Token', csrfToken)
  }
  let response: Response
  try {
    response = await fetch(url, {
      ...init,
      headers,
      credentials: 'same-origin',
    })
  } catch (error) {
    if (init.signal?.aborted) {
      throw error
    }
    throw new ApiError('network', error instanceof Error ? error.message : String(error))
  }
  if (!response.ok) {
    if (response.status === 401 && options.handleUnauthorized !== false && !isAuthAttempt(url)) {
      unauthorizedHandler?.()
    }
    throw await readErrorResponse(response)
  }
  return response
}

/** 用服务端保存的 Key 校验；请求体里只出现本次输入的候选 Key。 */
export const verifyApiKey = async (apiKey: string, signal?: AbortSignal): Promise<void> => {
  await request(
    '/api/v1/settings/api-key/verify',
    {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ apiKey }),
      signal,
    },
    { handleUnauthorized: false }
  )
}

export const uploadFile = async (
  file: File,
  purpose: FilePurpose | null,
  isImage: boolean,
  id: string,
  signal?: AbortSignal
): Promise<FileObject> => {
  const form = new FormData()
  // 服务端用这个 ID 保存原件，和消息里的附件 id 保持一致，刷新后可直接下载预览。
  form.append('id', id)
  if (purpose) {
    form.append('purpose', purpose)
  }
  form.append('isImage', String(isImage))
  form.append('file', isImage ? await resizeUploadImage(file, signal) : file)
  const response = await request('/api/v1/files', {
    method: 'POST',
    body: form,
    signal,
  })
  return (await response.json()) as FileObject
}

/** 删除服务端附件（元数据与磁盘原件）；用于移除尚未发送的草稿附件。 */
export const deleteFile = async (fileId: string): Promise<void> => {
  await request(`/api/v1/files/${encodeURIComponent(fileId)}`, { method: 'DELETE' })
}

/**
 * 服务端 SSE 代理：服务端注入 API Key 并把本地附件 ID 换成 DeepSeek file_id。
 * 逐个产出流式事件，收到 message_stop 时结束。
 */
export async function* streamMessage(
  body: CreateMessageRequest,
  signal: AbortSignal
): AsyncGenerator<MessageStreamEvent> {
  const response = await request(
    '/api/v1/stream',
    {
      method: 'POST',
      headers: {
        'Content-Type': 'application/json',
        Accept: 'text/event-stream',
      },
      body: JSON.stringify({ ...body, stream: true }),
      signal,
    },
    { handleUnauthorized: false }
  )
  const contentType = response.headers.get('Content-Type') ?? ''
  if (!response.body || !contentType.includes('text/event-stream')) {
    throw await readErrorResponse(response, 'protocol', '')
  }
  for await (const { event: eventName, data } of readServerSentEvents(response.body, signal)) {
    let payload: unknown
    try {
      payload = JSON.parse(data)
    } catch {
      if (eventName !== 'error') {
        continue
      }
    }
    if (isErrorBody(payload)) {
      throw new ApiError('stream', payload.error.message)
    }
    if (eventName === 'error') {
      throw new ApiError('stream', data)
    }
    const event = readMessageEvent(payload)
    if (!event) {
      continue
    }
    yield event
    if (event.type === 'message_stop') {
      return
    }
  }
  // 服务端没有提供错误内容时保持为空，不推测断流原因。
  throw new ApiError('interrupted', '')
}
