import { create } from 'zustand'
import { emptySequence, mergeSequence, type SequenceState } from '@/lib/sequence'
import { notificationHistory, roomMessages } from '@/lib/socialApi'
import type { ChatMessage, Notification, SequencePage } from '@/types/api'

interface LiveState {
  owner: string | null
  connected: boolean
  roomStreams: Record<string, SequenceState<ChatMessage>>
  notifications: SequenceState<Notification>
  online: Set<string>
  reset: (owner: string | null) => void
  setConnected: (connected: boolean) => void
  addMessages: (roomId: string, items: ChatMessage[]) => void
  addNotifications: (items: Notification[]) => void
  setPresence: (ids: string[]) => void
  updatePresence: (userId: string, online: boolean) => void
}

export const useRealtimeStore = create<LiveState>((set) => ({
  owner: null,
  connected: false,
  roomStreams: {},
  notifications: emptySequence(),
  online: new Set(),
  reset: (owner) => set({ owner, connected: false, roomStreams: {}, notifications: emptySequence(), online: new Set() }),
  setConnected: (connected) => set({ connected }),
  addMessages: (roomId, items) => set((state) => ({
    roomStreams: {
      ...state.roomStreams,
      [roomId]: mergeSequence(state.roomStreams[roomId] ?? emptySequence(), items, (item) => item.roomSeq),
    },
  })),
  addNotifications: (items) => set((state) => ({
    notifications: mergeSequence(state.notifications, items.map((item) => {
      const prior = state.notifications.items.get(item.userNotifSeq) ?? state.notifications.pending.get(item.userNotifSeq)
      return !item.deleted && prior?.isRead ? { ...item, isRead: true } : item
    }), (item) => item.userNotifSeq),
  })),
  setPresence: (ids) => set({ online: new Set(ids) }),
  updatePresence: (userId, online) => set((state) => {
    const users = new Set(state.online)
    if (online) users.add(userId)
    else users.delete(userId)
    return { online: users }
  }),
}))

const catchingUp = new Map<string, Promise<void>>()

function reconcile<T>(
  key: string,
  getSeq: () => number,
  read: (afterSeq: number) => Promise<SequencePage<T>>,
  merge: (items: T[]) => void,
): Promise<void> {
  const owner = useRealtimeStore.getState().owner
  if (!owner) return Promise.resolve()
  const scopedKey = `${owner}:${key}`
  const existing = catchingUp.get(scopedKey)
  if (existing) return existing
  const request = (async () => {
    let more = true
    while (more && useRealtimeStore.getState().owner === owner) {
      const afterSeq = getSeq()
      const page = await read(afterSeq)
      if (useRealtimeStore.getState().owner !== owner) return
      merge(page.data)
      more = page.hasMore && getSeq() > afterSeq
    }
  })().finally(() => catchingUp.delete(scopedKey))
  catchingUp.set(scopedKey, request)
  return request
}

export const catchUpRoom = (id: string) =>
  reconcile(
    `room:${id}`,
    () => useRealtimeStore.getState().roomStreams[id]?.lastContiguousSeq ?? 0,
    (seq) => roomMessages(id, seq),
    (items) => useRealtimeStore.getState().addMessages(id, items),
  )

export const catchUpNotifications = () =>
  reconcile(
    'notifications',
    () => useRealtimeStore.getState().notifications.lastContiguousSeq,
    notificationHistory,
    (items) => useRealtimeStore.getState().addNotifications(items),
  )
