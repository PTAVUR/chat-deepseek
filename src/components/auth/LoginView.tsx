import { useState, type FormEvent } from 'react'
import { useTranslate } from '../../i18n/index.js'
import { ApiError } from '../../services/api/client.js'
import { login } from '../../services/api/server.js'
import { WhaleLogo } from '../base/WhaleLogo.js'
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
      <main className={styles.content}>
        <section className={styles.formColumn}>
          <div className={styles.brand}>
            <WhaleLogo size={30} />
            <span className={styles.wordmark}>Deepseek</span>
          </div>
          <h1 className={styles.srOnly}>{t('loginTitle')}</h1>
          <form className={styles.form} onSubmit={(event) => void submit(event)}>
            <label className={styles.field}>
              <span className={styles.srOnly}>{t('loginUsername')}</span>
              <input
                className={styles.input}
                name="username"
                placeholder={t('loginUsername')}
                autoComplete="username"
                autoCapitalize="none"
                spellCheck={false}
                autoFocus
                required
                value={username}
                onChange={(event) => setUsername(event.target.value)}
              />
            </label>
            <label className={styles.field}>
              <span className={styles.srOnly}>{t('loginPassword')}</span>
              <input
                className={styles.input}
                name="password"
                type="password"
                placeholder={t('loginPassword')}
                autoComplete="current-password"
                required
                value={password}
                onChange={(event) => setPassword(event.target.value)}
              />
            </label>
            <p className={styles.description}>{t('loginDescription')}</p>
            {error ? (
              <div className={styles.error} role="alert">
                {error}
              </div>
            ) : null}
            <button type="submit" className={styles.submit} disabled={busy}>
              {busy ? t('loginPending') : t('loginAction')}
            </button>
          </form>
        </section>
        <aside className={styles.aside} aria-label={t('loginAsideTitle')}>
          <div className={styles.asideLogo}>
            <WhaleLogo size={44} />
          </div>
          <div className={styles.asideTitle}>{t('loginAsideTitle')}</div>
          <p className={styles.asideText}>{t('loginAsideDescription')}</p>
        </aside>
      </main>
      <footer className={styles.footer}>{t('loginFooter')}</footer>
    </div>
  )
}
