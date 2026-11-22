import { useTranslate } from '../../i18n/index.js'
import styles from './Settings.module.css'

/** 关于：纯文字说明，不提供任何操作。 */
export const AboutSettings = () => {
  const t = useTranslate()
  return <div className={styles.aboutText}>{t('aboutContent')}</div>
}
