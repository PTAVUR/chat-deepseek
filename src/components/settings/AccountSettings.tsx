import { useRef, useState } from 'react'
import { useTranslate, type TranslationKey } from '../../i18n/index.js'
import { ApiError } from '../../services/api/client.js'
import {
  changePassword,
  changeUsername,
  saveAvatar,
  type AuthUser,
} from '../../services/api/server.js'
import { fileToAvatarDataURL } from '../../utils/avatar.js'
import { Avatar } from '../base/Avatar.js'
import { Button } from '../base/Button.js'
import { showToast } from '../base/toastState.js'
import { SettingRow } from './SettingRow.js'
import styles from './Settings.module.css'

type Status = { text: string; error?: boolean } | null

/** 头像失败只通过红色 toast 提示，不占用设置行下方的位置。 */
const avatarErrorKey = (error: unknown): TranslationKey => {
  if (error instanceof Error && error.message === 'avatar too large') {
    return 'accountAvatarTooLarge'
  }
  if (error instanceof Error && error.message === 'not an image') {
    return 'accountAvatarInvalid'
  }
  if (error instanceof ApiError && error.status === 413) {
    return 'accountAvatarTooLarge'
  }
  if (error instanceof ApiError && error.status === 400) {
    return 'accountAvatarInvalid'
  }
  if (!(error instanceof ApiError)) {
    return 'accountAvatarInvalid'
  }
  return 'accountSaveFailed'
}

/** 账户管理：只允许修改自己的用户名、密码和头像；保存后就地更新，不关闭弹窗。 */
export const AccountSettings = ({
  user,
  onUserChange,
}: {
  user: AuthUser
  onUserChange: (user: AuthUser) => void
}) => {
  const t = useTranslate()
  const fileRef = useRef<HTMLInputElement>(null)
  const [username, setUsername] = useState(user.username)
  const [usernameBusy, setUsernameBusy] = useState(false)
  const [avatarBusy, setAvatarBusy] = useState(false)
  const [passwordOpen, setPasswordOpen] = useState(false)
  const [currentPassword, setCurrentPassword] = useState('')
  const [newPassword, setNewPassword] = useState('')
  const [confirmPassword, setConfirmPassword] = useState('')
  const [passwordBusy, setPasswordBusy] = useState(false)
  const [passwordStatus, setPasswordStatus] = useState<Status>(null)

  const submitUsername = async () => {
    const next = username.trim()
    if (!next || next === user.username || usernameBusy) {
      return
    }
    setUsernameBusy(true)
    try {
      await changeUsername(next)
      setUsername(next)
      onUserChange({ ...user, username: next })
    } catch (error) {
      const key: TranslationKey =
        error instanceof ApiError && error.status === 409
          ? 'accountUsernameTaken'
          : error instanceof ApiError && error.status === 400
            ? 'accountUsernameInvalid'
            : 'accountSaveFailed'
      showToast({ key, type: 'error' })
    } finally {
      setUsernameBusy(false)
    }
  }

  const uploadAvatar = async (file: File) => {
    if (avatarBusy) {
      return
    }
    setAvatarBusy(true)
    try {
      const data = await fileToAvatarDataURL(file)
      await saveAvatar(data)
      onUserChange({ ...user, avatar: data })
    } catch (error) {
      showToast({ key: avatarErrorKey(error), type: 'error' })
    } finally {
      setAvatarBusy(false)
    }
  }

  const removeAvatar = async () => {
    if (avatarBusy) {
      return
    }
    setAvatarBusy(true)
    try {
      await saveAvatar('')
      onUserChange({ ...user, avatar: '' })
    } catch {
      showToast({ key: 'accountSaveFailed', type: 'error' })
    } finally {
      setAvatarBusy(false)
    }
  }

  const openPassword = () => {
    setPasswordStatus(null)
    setPasswordOpen(true)
  }

  const cancelPassword = () => {
    setCurrentPassword('')
    setNewPassword('')
    setConfirmPassword('')
    setPasswordStatus(null)
    setPasswordOpen(false)
  }

  const submitPassword = async () => {
    if (passwordBusy || !currentPassword || !newPassword || !confirmPassword) {
      return
    }
    if (newPassword !== confirmPassword) {
      setPasswordStatus({ text: t('accountPasswordMismatch'), error: true })
      return
    }
    setPasswordBusy(true)
    setPasswordStatus(null)
    try {
      await changePassword(currentPassword, newPassword)
      setCurrentPassword('')
      setNewPassword('')
      setConfirmPassword('')
      setPasswordOpen(false)
      setPasswordStatus({ text: t('accountPasswordUpdated') })
    } catch (error) {
      if (error instanceof ApiError && error.status === 401) {
        setPasswordStatus({ text: t('accountPasswordWrong'), error: true })
      } else if (error instanceof ApiError && error.status === 400) {
        setPasswordStatus({ text: t('accountPasswordTooShort'), error: true })
      } else {
        setPasswordStatus({ text: t('accountSaveFailed'), error: true })
      }
    } finally {
      setPasswordBusy(false)
    }
  }

  return (
    <>
      <SettingRow
        label={t('accountAvatar')}
        action={
          <>
            <Avatar
              className={styles.avatarPreview}
              username={user.username}
              avatar={user.avatar}
              size={40}
            />
            <Button
              variant="outlined"
              size="m"
              disabled={avatarBusy}
              onClick={() => fileRef.current?.click()}
            >
              {t('accountUploadAvatar')}
            </Button>
            {user.avatar ? (
              <Button
                variant="outlined"
                size="m"
                disabled={avatarBusy}
                onClick={() => void removeAvatar()}
              >
                {t('accountRemoveAvatar')}
              </Button>
            ) : null}
          </>
        }
      >
        <input
          ref={fileRef}
          className={styles.hiddenInput}
          type="file"
          accept="image/png,image/jpeg,image/webp"
          onChange={(event) => {
            const file = event.target.files?.[0]
            event.target.value = ''
            if (file) {
              void uploadAvatar(file)
            }
          }}
        />
      </SettingRow>
      <SettingRow
        label={t('accountUsername')}
        action={
          <>
            <input
              className={styles.accountInput}
              name="username"
              aria-label={t('accountUsername')}
              autoComplete="username"
              maxLength={64}
              value={username}
              onChange={(event) => setUsername(event.target.value)}
              onKeyDown={(event) => {
                if (event.key === 'Enter' && !event.nativeEvent.isComposing) {
                  void submitUsername()
                }
              }}
            />
            <Button
              variant="outlined"
              size="m"
              disabled={usernameBusy || !username.trim() || username.trim() === user.username}
              onClick={() => void submitUsername()}
            >
              {t('accountSave')}
            </Button>
          </>
        }
      />
      <SettingRow
        label={t('accountPasswordTitle')}
        description={t('accountPasswordDescription')}
        status={passwordStatus?.text}
        statusTone={passwordStatus?.error ? 'error' : passwordStatus ? 'success' : undefined}
        className={styles.passwordCell}
        action={
          passwordOpen ? undefined : (
            <Button variant="outlined" size="m" onClick={openPassword}>
              {t('accountChangePassword')}
            </Button>
          )
        }
      >
        {passwordOpen ? (
          <div className={styles.passwordPanel}>
            <div className={styles.passwordFields}>
              <input
                className={styles.accountInput}
                type="password"
                name="current-password"
                aria-label={t('accountCurrentPassword')}
                placeholder={t('accountCurrentPassword')}
                autoComplete="current-password"
                value={currentPassword}
                onChange={(event) => setCurrentPassword(event.target.value)}
              />
              <input
                className={styles.accountInput}
                type="password"
                name="new-password"
                aria-label={t('accountNewPassword')}
                placeholder={t('accountNewPassword')}
                autoComplete="new-password"
                value={newPassword}
                onChange={(event) => setNewPassword(event.target.value)}
              />
              <input
                className={styles.accountInput}
                type="password"
                name="confirm-password"
                aria-label={t('accountConfirmPassword')}
                placeholder={t('accountConfirmPassword')}
                autoComplete="new-password"
                value={confirmPassword}
                onChange={(event) => setConfirmPassword(event.target.value)}
              />
            </div>
            <div className={styles.passwordActions}>
              <Button
                variant="primary"
                size="m"
                disabled={passwordBusy || !currentPassword || !newPassword || !confirmPassword}
                onClick={() => void submitPassword()}
              >
                {t('accountChangePassword')}
              </Button>
              <Button variant="outlined" size="m" disabled={passwordBusy} onClick={cancelPassword}>
                {t('cancel')}
              </Button>
            </div>
          </div>
        ) : null}
      </SettingRow>
    </>
  )
}
