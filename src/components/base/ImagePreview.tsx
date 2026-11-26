import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from 'react'
import { createPortal } from 'react-dom'
import { useTranslate } from '../../i18n/index.js'
import { IconCloseOutline16 } from './icons.js'
import styles from './ImagePreview.module.css'

export type PreviewImage = {
  src: string
  alt?: string
}

type ImagePreviewContextValue = {
  openImage: (image: PreviewImage) => void
}

const ImagePreviewContext = createContext<ImagePreviewContextValue | null>(null)

export const useImagePreview = () => {
  const value = useContext(ImagePreviewContext)
  if (!value) {
    throw new Error('ImagePreviewProvider is missing')
  }
  return value
}

/** 全屏图片预览：点击遮罩、关闭按钮或按 Esc 退出。 */
export const ImagePreviewProvider = ({ children }: { children: ReactNode }) => {
  const t = useTranslate()
  const [image, setImage] = useState<PreviewImage | null>(null)
  const openImage = useCallback((next: PreviewImage) => setImage(next), [])
  const close = useCallback(() => setImage(null), [])

  useEffect(() => {
    if (!image) {
      return
    }
    const previousOverflow = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') {
        close()
      }
    }
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.body.style.overflow = previousOverflow
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [image, close])

  const contextValue = useMemo(() => ({ openImage }), [openImage])

  return (
    <ImagePreviewContext.Provider value={contextValue}>
      {children}
      {image
        ? createPortal(
            <div
              className={styles.overlay}
              role="dialog"
              aria-modal="true"
              aria-label={image.alt || t('imagePreview')}
              onClick={close}
            >
              <button
                type="button"
                className={styles.close}
                aria-label={t('close')}
                onClick={(event) => {
                  event.stopPropagation()
                  close()
                }}
              >
                <IconCloseOutline16 />
              </button>
              <img
                className={styles.image}
                src={image.src}
                alt={image.alt ?? ''}
                draggable={false}
                onClick={(event) => event.stopPropagation()}
              />
              {image.alt ? (
                <div className={styles.caption} onClick={(event) => event.stopPropagation()}>
                  {image.alt}
                </div>
              ) : null}
            </div>,
            document.body
          )
        : null}
    </ImagePreviewContext.Provider>
  )
}
