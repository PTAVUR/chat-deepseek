import clsx from 'clsx'
import styles from './Avatar.module.css'

/** 圆形头像：有图片时显示图片，否则显示用户名首字母。 */
export const Avatar = ({
  username,
  avatar,
  size = 32,
  className,
}: {
  username: string
  avatar?: string
  size?: number
  className?: string
}) => {
  const style = { width: size, height: size }
  if (avatar) {
    return (
      <img
        className={clsx(styles.avatar, className)}
        style={style}
        src={avatar}
        alt=""
        aria-hidden={true}
      />
    )
  }
  return (
    <span
      className={clsx(styles.avatar, className)}
      style={{ ...style, fontSize: Math.max(12, Math.round(size * 0.44)) }}
      aria-hidden={true}
    >
      {username.slice(0, 1).toUpperCase() || '?'}
    </span>
  )
}
