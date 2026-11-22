import { apiFetch } from '@/lib/api'
import type {
  ChatMessage, ChatRoom, MediaAttachment, MediaVisibility, Notification, SequencePage,
} from '@/types/api'

export function send<T>(path: string, method: 'POST' | 'PATCH' | 'DELETE', body?: unknown): Promise<T> {
  return apiFetch<T>(path, { method, body: body === undefined ? undefined : JSON.stringify(body) })
}

const uploadMimeTypes = ['image/jpeg', 'image/png', 'image/webp', 'image/gif', 'video/mp4']
export const mediaAccept = uploadMimeTypes.join(',')

export async function uploadMedia(file: File, visibility: MediaVisibility): Promise<MediaAttachment> {
  if (!uploadMimeTypes.includes(file.type) || file.size === 0 || file.size > 5 * 1024 * 1024) {
    throw new Error('Choose a JPEG, PNG, WebP, GIF, or MP4 file up to 5 MB.')
  }
  const upload = await send<{ mediaId: string; uploadUrl: string; headers: Record<string, string[]> }>(
    '/api/media/presign', 'POST', { mimeType: file.type, sizeBytes: file.size, visibility },
  )
  const headers = new Headers()
  for (const [name, values] of Object.entries(upload.headers)) {
    for (const value of values) headers.append(name, value)
  }
  const response = await fetch(upload.uploadUrl, { method: 'PUT', headers, body: file })
  if (!response.ok) throw new Error('The file could not be uploaded.')
  return send<MediaAttachment>('/api/media/finalize', 'POST', { mediaId: upload.mediaId })
}

export const chatRoomsKey = ['chat', 'rooms'] as const
export const unreadNotificationsKey = ['notifications', 'unread'] as const

export const chatRooms = () => apiFetch<ChatRoom[]>('/api/chat/rooms/sync')
export const unreadNotifications = () => apiFetch<{ unreadCount: number }>('/api/notifications/unread-count')
export const roomMessages = (roomId: string, afterSeq: number) =>
  apiFetch<SequencePage<ChatMessage>>(`/api/chat/rooms/${roomId}/messages?afterSeq=${afterSeq}`)
export const notificationHistory = (afterSeq: number) =>
  apiFetch<SequencePage<Notification>>(`/api/notifications?afterSeq=${afterSeq}`)
