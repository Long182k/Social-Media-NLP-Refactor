import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Bell } from 'lucide-react'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { dayjs } from '@/lib/date'
import { send, unreadNotificationsKey } from '@/lib/socialApi'
import { catchUpNotifications, useRealtimeStore } from '@/stores/realtimeStore'
import type { Notification } from '@/types/api'

function target(item: Notification) {
  if (!item.entityId) return null
  if (item.type === 'FOLLOW') return `/profile/${item.entityId}`
  if (item.type?.includes('GROUP')) return `/groups/${item.entityId}`
  if (item.type?.includes('EVENT')) return `/events/${item.entityId}`
  if (item.type === 'BOOKMARK' || item.type === 'COMMENT' || item.type === 'LIKE') return `/posts/${item.entityId}`
  return null
}

export default function NotificationsPage() {
  const stream = useRealtimeStore((state) => state.notifications)
  const add = useRealtimeStore((state) => state.addNotifications)
  const owner = useRealtimeStore((state) => state.owner)
  const client = useQueryClient()
  const [error, setError] = useState<unknown>(null)
  const items = useMemo(() => Array.from(stream.items.values()).filter((item) => !item.deleted).reverse(), [stream.items])

  useEffect(() => {
    let live = true
    if (owner) void catchUpNotifications().catch((err: unknown) => { if (live) setError(err) })
    return () => { live = false }
  }, [owner])

  const update = useMutation({
    mutationFn: async ({ mode, item }: { mode: 'all' | 'read' | 'delete'; item?: Notification }) => {
      if (mode === 'all') {
        await send('/api/notifications/read', 'PATCH')
        add(items.map((notice) => ({ ...notice, isRead: true })))
      } else if (item && mode === 'read') {
        add([await send<Notification>(`/api/notifications/${item.id}/read`, 'PATCH')])
      } else if (item) {
        await send(`/api/notifications/${item.id}`, 'DELETE')
        add([{ id: item.id, userNotifSeq: item.userNotifSeq, deleted: true }])
      }
    },
    onSuccess: () => client.invalidateQueries({ queryKey: unreadNotificationsKey }),
  })

  return (
    <Section title="Notifications" action={<Button variant="outline" disabled={update.isPending || !items.some((item) => !item.isRead)} onClick={() => update.mutate({ mode: 'all' })}>Mark all as read</Button>}>
      <RequestError error={error || update.error} />
      {items.length === 0 && <EmptyState>You're caught up.</EmptyState>}
      <div className="space-y-2">
        {items.map((item) => {
          const to = target(item)
          return (
            <Card key={item.id} className={!item.isRead ? 'border-primary/40' : undefined}>
              <CardContent className="flex flex-wrap items-start gap-3 p-4">
                <Bell className="mt-1 h-4 w-4 text-primary" aria-hidden="true" />
                <div className="min-w-0 flex-1">
                  {to ? <Link to={to} className="text-sm font-medium hover:underline">{item.content}</Link> : <p className="text-sm font-medium">{item.content}</p>}
                  {item.createdAt && <p className="text-xs text-muted-foreground">{dayjs(item.createdAt).fromNow()}{item.isRead ? '' : ' · Unread'}</p>}
                </div>
                {!item.isRead && <Button variant="ghost" size="sm" disabled={update.isPending} onClick={() => update.mutate({ mode: 'read', item })}>Mark as read</Button>}
                <Button variant="ghost" size="sm" disabled={update.isPending} onClick={() => update.mutate({ mode: 'delete', item })}>Delete notification</Button>
              </CardContent>
            </Card>
          )
        })}
      </div>
    </Section>
  )
}
