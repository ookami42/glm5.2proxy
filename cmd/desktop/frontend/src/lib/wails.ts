import { BrowserOpenURL } from '../../wailsjs/runtime/runtime'

export function openExternalURL(url: string): void {
  if (!/^https?:\/\//i.test(url)) {
    throw new Error('A URL de autenticação retornada é inválida')
  }

  try {
    BrowserOpenURL(url)
    return
  } catch {
    // Browser preview does not expose the Wails runtime.
  }

  const opened = window.open(url, '_blank', 'noopener,noreferrer')
  if (!opened) {
    throw new Error('Não foi possível abrir o navegador para autenticação')
  }
}
