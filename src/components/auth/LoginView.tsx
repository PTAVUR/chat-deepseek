import { useState, type FormEvent } from 'react'
import { useTranslate } from '../../i18n/index.js'
import { ApiError } from '../../services/api/client.js'
import { login } from '../../services/api/server.js'
import { Button } from '../base/Button.js'
import styles from './LoginView.module.css'

/** 服务端账户登录；成功后整页重载，确保上一个账户的内存状态全部丢弃。 */
export const LoginView = () => {
  const t = useTranslate()
  const [username, setUsername] = useState('')
  const [password, setPassword] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (busy || !username.trim() || !password) {
      return
    }
    setBusy(true)
    setError(null)
    try {
      await login(username.trim(), password)
      window.location.assign('/')
    } catch (e) {
      if (e instanceof ApiError) {
        setError(
          e.status === 401
            ? t('loginFailed')
            : e.status === undefined
              ? t('loginServerUnavailable')
              : e.message
        )
      } else {
        setError(t('loginServerUnavailable'))
      }
      setBusy(false)
    }
  }

  return (
    <div className={styles.screen}>
      <div className={styles.card}>
        <div className={styles.brand}>chat-deepseek</div>
        <h1 className={styles.title}>{t('loginTitle')}</h1>
        <p className={styles.description}>{t('loginDescription')}</p>
        <form className={styles.form} onSubmit={(event) => void submit(event)}>
          <label className={styles.field}>
            <span className={styles.label}>{t('loginUsername')}</span>
            <input
              className={styles.input}
              name="username"
              autoComplete="username"
              autoCapitalize="none"
              spellCheck={false}
              autoFocus
              value={username}
              onChange={(event) => setUsername(event.target.value)}
            />
          </label>
          <label className={styles.field}>
            <span className={styles.label}>{t('loginPassword')}</span>
            <input
              className={styles.input}
              name="password"
              type="password"
              autoComplete="current-password"
              value={password}
              onChange={(event) => setPassword(event.target.value)}
            />
          </label>
          {error ? (
            <div className={styles.error} role="alert">
              {error}
            </div>
          ) : null}
          <Button
            type="submit"
            variant="primary"
            size="l"
            block
            disabled={busy || !username.trim() || !password}
          >
            {busy ? t('loginPending') : t('loginAction')}
          </Button>
        </form>
      </div>
    </div>
  )
}
