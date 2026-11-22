import { useEffect, useState } from 'react'
import { useTranslate } from '../i18n/index.js'
import { getMe } from '../services/api/server.js'
import { onUnauthorized } from '../services/api/client.js'
import { loadSettings } from '../services/settings.js'
import { LoginView } from '../components/auth/LoginView.js'
import { Button } from '../components/base/Button.js'
import { App } from './App.js'
import styles from './Root.module.css'

type Phase = 'loading' | 'login' | 'ready' | 'error'

/** 认证门：先确认 Cookie 会话，再加载服务端设置，最后挂载聊天界面。 */
export const Root = () => {
  const t = useTranslate()
  const [phase, setPhase] = useState<Phase>('loading')
  const [attempt, setAttempt] = useState(0)

  // 会话在使用过程中失效时整页重载，回到登录页并清空上一账户的内存数据。
  useEffect(
    () =>
      onUnauthorized(() => {
        window.location.assign('/')
      }),
    []
  )

  useEffect(() => {
    let active = true
    setPhase('loading')
    void (async () => {
      try {
        const me = await getMe()
        if (!active) {
          return
        }
        if (!me) {
          setPhase('login')
          return
        }
        await loadSettings()
        if (active) {
          setPhase('ready')
        }
      } catch {
        if (active) {
          setPhase('error')
        }
      }
    })()
    return () => {
      active = false
    }
  }, [attempt])

  if (phase === 'ready') {
    return <App />
  }
  if (phase === 'login') {
    return <LoginView />
  }

  return (
    <div className={styles.screen}>
      {phase === 'error' ? (
        <div className={styles.error} role="alert">
          <span>{t('loginServerUnavailable')}</span>
          <Button variant="outlined" size="m" onClick={() => setAttempt((value) => value + 1)}>
            {t('loginRetry')}
          </Button>
        </div>
      ) : (
        <div className={styles.loading} role="status" aria-live="polite">
          {t('loginLoading')}
        </div>
      )}
    </div>
  )
}
