import { useEffect } from 'react'
import { useQueryClient } from '@tanstack/react-query'
import { ApiError, broadcastSessionCleared, refreshAccessToken } from '@/lib/api'
import { chatRoomsKey, send, unreadNotificationsKey } from '@/lib/socialApi'
import { catchUpNotifications, useRealtimeStore } from '@/stores/realtimeStore'
import { useUIStore } from '@/stores/uiStore'
import type { ChatMessage, Notification } from '@/types/api'

type RealtimeEvent =
  | { type: 'NEW_MESSAGE'; payload: ChatMessage }
  | { type: 'NOTIFICATION_CREATED' | 'NOTIFICATION_DELETED'; payload: Notification }
  | { type: 'PRESENCE_SNAPSHOT'; payload: string[] }
  | { type: 'PRESENCE_UPDATE'; payload: { userId: string; online: boolean } }
  | { type: 'POST_SENTIMENT_UPDATED' | 'COMMENT_SENTIMENT_UPDATED'; payload: unknown }

async function issueTicket() {
  try {
    return await send<{ ticket: string }>('/api/auth/ws-ticket', 'POST')
  } catch (error) {
    if (error instanceof ApiError && error.status === 401 && await refreshAccessToken()) {
      return send<{ ticket: string }>('/api/auth/ws-ticket', 'POST')
    }
    throw error
  }
}

export function useRealtime(owner: string | undefined) {
  const client = useQueryClient()
  useEffect(() => {
    useRealtimeStore.getState().reset(owner ?? null)
    if (!owner) return

    let stopped = false
    let socket: WebSocket | null = null
    let retryTimer: number | undefined
    let delay = 500

    function retry() {
      if (stopped) return
      retryTimer = window.setTimeout(() => void connect(), delay)
      delay = Math.min(delay * 2, 10_000)
    }

    async function connect() {
      if (stopped) return
      try {
        const { ticket } = await issueTicket()
        if (stopped) return
        const url = new URL('/ws', window.location.href)
        url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
        url.searchParams.set('ticket', ticket)
        socket = new WebSocket(url)
        socket.onopen = () => {
          if (stopped) return
          delay = 500
          useRealtimeStore.getState().setConnected(true)
          void client.invalidateQueries({ queryKey: chatRoomsKey })
          void client.invalidateQueries({ queryKey: unreadNotificationsKey })
          void catchUpNotifications().catch(() => undefined)
        }
        socket.onclose = () => {
          if (stopped) return
          useRealtimeStore.getState().setConnected(false)
          retry()
        }
        socket.onmessage = ({ data }: MessageEvent<string>) => {
          if (stopped) return
          let event: RealtimeEvent
          try {
            event = JSON.parse(data) as RealtimeEvent
          } catch {
            return
          }
          const store = useRealtimeStore.getState()
          switch (event.type) {
            case 'NEW_MESSAGE':
              store.addMessages(event.payload.roomId, [event.payload])
              void client.invalidateQueries({ queryKey: chatRoomsKey })
              break
            case 'NOTIFICATION_CREATED':
            case 'NOTIFICATION_DELETED':
              store.addNotifications([event.payload])
              if (useRealtimeStore.getState().notifications.pending.size) {
                void catchUpNotifications().catch(() => undefined)
              }
              void client.invalidateQueries({ queryKey: unreadNotificationsKey })
              break
            case 'PRESENCE_SNAPSHOT':
              store.setPresence(event.payload)
              break
            case 'PRESENCE_UPDATE':
              store.updatePresence(event.payload.userId, event.payload.online)
              break
            case 'POST_SENTIMENT_UPDATED':
              void client.invalidateQueries({ queryKey: ['posts'] })
              break
            case 'COMMENT_SENTIMENT_UPDATED':
              void client.invalidateQueries({ queryKey: ['comments'] })
              break
          }
        }
      } catch (error) {
        if (stopped) return
        if (error instanceof ApiError && (error.status === 401 || error.status === 403)) {
          useUIStore.getState().clearSession()
          broadcastSessionCleared()
          client.clear()
          return
        }
        retry()
      }
    }
    void connect()
    return () => {
      stopped = true
      window.clearTimeout(retryTimer)
      if (socket?.readyState === WebSocket.CONNECTING) {
        socket.onopen = () => socket?.close()
        socket.onmessage = null
      } else {
        socket?.close()
      }
      useRealtimeStore.getState().reset(null)
    }
  }, [owner, client])
}
