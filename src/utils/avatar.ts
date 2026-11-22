const MAX_AVATAR_EDGE = 128
const MAX_AVATAR_FILE_SIZE = 20 * 1024 * 1024

/** 把本地图片裁成正方形并压缩成 128px 的 data URL，供服务端存储。 */
export const fileToAvatarDataURL = async (file: File): Promise<string> => {
  if (!file.type.startsWith('image/')) {
    throw new Error('not an image')
  }
  if (file.size > MAX_AVATAR_FILE_SIZE) {
    throw new Error('avatar too large')
  }
  const image = new Image()
  const url = URL.createObjectURL(file)
  try {
    image.src = url
    await image.decode()
  } finally {
    URL.revokeObjectURL(url)
  }

  const edge = Math.min(image.naturalWidth, image.naturalHeight)
  if (edge <= 0) {
    throw new Error('empty image')
  }
  const canvas = document.createElement('canvas')
  canvas.width = MAX_AVATAR_EDGE
  canvas.height = MAX_AVATAR_EDGE
  try {
    const context = canvas.getContext('2d')
    if (!context) {
      throw new Error('cannot create canvas')
    }
    // 从原图中心裁出正方形，再缩放到固定尺寸，横图和竖图都不会被拉变形。
    context.drawImage(
      image,
      (image.naturalWidth - edge) / 2,
      (image.naturalHeight - edge) / 2,
      edge,
      edge,
      0,
      0,
      MAX_AVATAR_EDGE,
      MAX_AVATAR_EDGE
    )
    return canvas.toDataURL('image/webp', 0.9)
  } finally {
    canvas.width = canvas.height = 0
  }
}
