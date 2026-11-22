import clsx from 'clsx'
import type { ReactNode } from 'react'
import styles from './Settings.module.css'

/**
 * 设置项的统一行：左侧标签与说明，右侧当前值或操作。
 * 说明与状态分开显示，避免保存反馈把原有说明顶掉。
 */
export const SettingRow = ({
  label,
  description,
  status,
  statusTone,
  action,
  className,
  children,
}: {
  label: string
  description?: string
  status?: ReactNode
  statusTone?: 'error' | 'success'
  action?: ReactNode
  className?: string
  /** 行下方展开的内容，例如修改密码表单 */
  children?: ReactNode
}) => (
  <div className={clsx(styles.cell, className)}>
    <div className={styles.text}>
      <div className={styles.label}>{label}</div>
      {description ? <div className={styles.description}>{description}</div> : null}
      {status ? (
        <div
          className={clsx(
            styles.status,
            statusTone === 'error' && styles.error,
            statusTone === 'success' && styles.success
          )}
          role={statusTone === 'error' ? 'alert' : 'status'}
        >
          {status}
        </div>
      ) : null}
    </div>
    {action ? <div className={styles.control}>{action}</div> : null}
    {children}
  </div>
)
