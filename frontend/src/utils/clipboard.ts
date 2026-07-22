/**
 * 剪贴板工具：兼容 HTTP（非安全上下文）与 HTTPS 环境
 *
 * 安全上下文（HTTPS/localhost）优先用 Clipboard API；
 * 非安全上下文（HTTP）回退到 execCommand('copy')，必须保持在用户手势上下文中同步执行。
 */

// 兼容 HTTP 环境的同步复制回退方案
const fallbackCopy = (text: string): boolean => {
  try {
    const textarea = document.createElement('textarea')
    textarea.value = text
    textarea.style.position = 'fixed'
    textarea.style.left = '-9999px'
    textarea.style.top = '0'
    textarea.style.opacity = '0'
    document.body.appendChild(textarea)
    textarea.focus()
    textarea.select()
    const ok = document.execCommand('copy')
    document.body.removeChild(textarea)
    return ok
  } catch {
    return false
  }
}

/**
 * 复制文本到剪贴板，返回是否成功
 */
export const copyToClipboard = (text: string): Promise<boolean> => {
  if (window.isSecureContext && navigator.clipboard) {
    return navigator.clipboard.writeText(text).then(() => true).catch(() => fallbackCopy(text))
  }
  return Promise.resolve(fallbackCopy(text))
}

/**
 * UTF-8 安全的 base64 编码（替代已废弃的 btoa(unescape(encodeURIComponent(...))) ）
 */
export const utf8ToBase64 = (str: string): string => {
  const bytes = new TextEncoder().encode(str)
  let binary = ''
  bytes.forEach((b) => { binary += String.fromCharCode(b) })
  return btoa(binary)
}
