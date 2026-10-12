import { onBeforeUnmount, ref } from 'vue'
import type { FileOperation, VideoThumbnailJob } from '@/types/files'
import { appWebSocketURL } from '@/base'

type ServerMessage =
  | { type: 'ready' }
  | { type: 'subscribed'; path: string }
  | { type: 'unsubscribed' }
  | { type: 'directory-invalidated'; path: string; reason?: string; at?: string }
  | { type: 'operation-updated'; operation: FileOperation }
  | { type: 'thumbnail-job-updated'; thumbnail_job: VideoThumbnailJob }
  | { type: 'error'; path?: string; code?: string; message?: string }

export function useRealtime(options: {
  onInvalidate: (path: string) => void
  onOperation: (operation: FileOperation) => void
  onThumbnailJob?: (job: VideoThumbnailJob) => void
  onConnected?: () => void
}) {
  let socket: WebSocket | null = null
  let targetPath = '/'
  let active = false
  let reconnectTimer: number | null = null
  let retryDelayMs = 500
  const connected = ref(false)

  function start(path: string) {
    targetPath = path
    active = true
    ensureConnected()
    sendSubscription()
  }

  function setDirectory(path: string) {
    targetPath = path
    sendSubscription()
  }

  function cancelOperation(id: string) {
    if (!socket || socket.readyState !== WebSocket.OPEN) return false
    socket.send(JSON.stringify({ type: 'cancel-operation', operation_id: id }))
    return true
  }

  function stop() {
    active = false
    connected.value = false
    if (reconnectTimer !== null) {
      window.clearTimeout(reconnectTimer)
      reconnectTimer = null
    }
    const current = socket
    socket = null
    if (current && (current.readyState === WebSocket.OPEN || current.readyState === WebSocket.CONNECTING)) {
      current.close(1000, 'component unmounted')
    }
  }

  function ensureConnected() {
    if (!active || socket) return

    socket = new WebSocket(appWebSocketURL('/api/fs/ws'))
    socket.onopen = () => {
      connected.value = true
      retryDelayMs = 500
      sendSubscription()
      options.onConnected?.()
    }
    socket.onmessage = event => {
      let message: ServerMessage
      try {
        message = JSON.parse(event.data as string) as ServerMessage
      } catch {
        return
      }
      if (message.type === 'directory-invalidated') {
        options.onInvalidate(message.path)
      } else if (message.type === 'operation-updated') {
        options.onOperation(message.operation)
      } else if (message.type === 'thumbnail-job-updated') {
        options.onThumbnailJob?.(message.thumbnail_job)
      }
    }
    socket.onerror = () => socket?.close()
    socket.onclose = () => {
      connected.value = false
      socket = null
      if (active) scheduleReconnect()
    }
  }

  function sendSubscription() {
    if (!socket || socket.readyState !== WebSocket.OPEN) return
    if (targetPath === '/') {
      socket.send(JSON.stringify({ type: 'unsubscribe-directory' }))
      return
    }
    socket.send(JSON.stringify({ type: 'subscribe-directory', path: targetPath }))
  }

  function scheduleReconnect() {
    if (reconnectTimer !== null) return
    reconnectTimer = window.setTimeout(() => {
      reconnectTimer = null
      ensureConnected()
    }, retryDelayMs)
    retryDelayMs = Math.min(retryDelayMs * 2, 10_000)
  }

  onBeforeUnmount(stop)
  return { start, setDirectory, stop, connected, cancelOperation }
}
