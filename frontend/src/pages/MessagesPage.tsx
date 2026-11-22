import { useEffect, useMemo, useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { MediaView } from '@/components/social/MediaView'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { Avatar } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { apiFetch } from '@/lib/api'
import { dayjs } from '@/lib/date'
import { chatRooms, chatRoomsKey, mediaAccept, send, uploadMedia } from '@/lib/socialApi'
import { cn } from '@/lib/utils'
import { catchUpRoom, useRealtimeStore } from '@/stores/realtimeStore'
import { useUIStore } from '@/stores/uiStore'
import type { ChatMessage, ChatRoom, MediaAttachment, ProfileSummary } from '@/types/api'

function roomName(room: ChatRoom, userId: string | undefined) {
  if (room.type === 'GROUP') return room.name
  return room.participants.find((member) => member.id !== userId)?.nickName ?? room.name
}

export default function MessagesPage() {
  const { id = '' } = useParams()
  const viewer = useUIStore((state) => state.user)
  const owner = useRealtimeStore((state) => state.owner)
  const connected = useRealtimeStore((state) => state.connected)
  const stream = useRealtimeStore((state) => state.roomStreams[id])
  const online = useRealtimeStore((state) => state.online)
  const addMessages = useRealtimeStore((state) => state.addMessages)
  const client = useQueryClient()
  const navigate = useNavigate()
  const [compose, setCompose] = useState(false)
  const [error, setError] = useState<unknown>(null)
  const [sharedTab, setSharedTab] = useState<'media' | 'links'>('media')
  const rooms = useQuery({ queryKey: chatRoomsKey, queryFn: chatRooms })
  const room = rooms.data?.find((item) => item.id === id)
  const lastSeen = stream?.lastContiguousSeq ?? 0
  const remoteLast = room?.lastSeq ?? 0
  const lastRead = room?.lastReadSeq ?? 0
  const pending = stream?.pending.size ?? 0
  const roomExists = !!room
  const messages = useMemo(() => Array.from(stream?.items.values() ?? []).filter((item) => !item.deleted), [stream])
  const shared = useQuery({
    queryKey: ['chat', id, 'shared'],
    queryFn: () => apiFetch<{ media: MediaAttachment[]; links: string[] }>(`/api/chat/rooms/${id}/shared`),
    enabled: roomExists,
  })

  useEffect(() => {
    if (!owner || !roomExists || (!pending && lastSeen >= remoteLast)) return
    let active = true
    void catchUpRoom(id).catch((err: unknown) => { if (active) setError(err) })
    return () => { active = false }
  }, [connected, id, lastSeen, owner, pending, remoteLast, roomExists])

  useEffect(() => {
    if (!roomExists || lastSeen <= lastRead) return
    let active = true
    void send(`/api/chat/rooms/${id}/read`, 'PATCH', { throughSeq: lastSeen }).then(() => {
      if (!active) return
      client.setQueryData<ChatRoom[]>(chatRoomsKey, (items) => items?.map((item) => (
        item.id === id ? { ...item, lastReadSeq: lastSeen, unreadCount: item.lastSeq <= lastSeen ? 0 : item.unreadCount } : item
      )))
    }).catch((err: unknown) => { if (active) setError(err) })
    return () => { active = false }
  }, [client, id, lastRead, lastSeen, roomExists])

  useEffect(() => {
    if (lastSeen > 0) void client.invalidateQueries({ queryKey: ['chat', id, 'shared'] })
  }, [client, id, lastSeen])

  const publish = useMutation({
    mutationFn: async ({ data }: { data: FormData; form: HTMLFormElement }) => {
      const file = data.get('attachment')
      const media = file instanceof File && file.size ? await uploadMedia(file, 'PRIVATE') : null
      return send<ChatMessage>(`/api/chat/rooms/${id}/messages`, 'POST', {
        content: data.get('message'), mediaIds: media ? [media.mediaId] : [],
      })
    },
    onSuccess: (message, { form }) => {
      form.reset()
      addMessages(message.roomId, [message])
      return client.invalidateQueries({ queryKey: chatRoomsKey })
    },
  })
  return (
    <Section title="Messages" action={<Button onClick={() => setCompose(true)}>New conversation</Button>}>
      <p role="status" className="text-xs text-muted-foreground">{connected ? 'Live updates connected' : 'Connecting to live updates…'}</p>
      <RequestError error={rooms.error || error} />
      <div className="grid gap-4 lg:grid-cols-[220px_minmax(0,1fr)_195px]">
        <aside aria-label="Conversations" className="space-y-2">
          {rooms.isSuccess && rooms.data.length === 0 && <EmptyState>No conversations yet.</EmptyState>}
          {rooms.data?.map((item) => (
            <Link key={item.id} aria-current={item.id === id ? 'page' : undefined} to={`/messages/${item.id}`}
              className={cn('block rounded-lg border border-border bg-card p-3 focus-visible:ring-2 focus-visible:ring-ring hover:bg-muted', item.id === id && 'border-primary/70')}>
              <div className="flex items-center justify-between gap-2">
                <span className="min-w-0 truncate text-sm font-semibold">{roomName(item, viewer?.id)}</span>
                {item.unreadCount > 0 && <span className="rounded-full bg-primary px-1.5 text-xs text-primary-foreground">{item.unreadCount}</span>}
              </div>
              {item.lastMessage && <p className="truncate text-xs text-muted-foreground">{item.lastMessage.content || 'Attachment'}</p>}
            </Link>
          ))}
        </aside>
        {room ? <>
          <Card className="min-w-0">
            <CardContent className="space-y-4 p-4">
              <div className="border-b border-border pb-3">
                <h2 className="font-semibold">{roomName(room, viewer?.id)}</h2>
                <p className="text-xs text-muted-foreground">
                  {room.participants.filter((person) => person.id !== viewer?.id && online.has(person.id)).length} other participants online
                </p>
              </div>
              <ol aria-label="Messages" className="max-h-[50dvh] space-y-2 overflow-y-auto">
                {messages.map((message) => (
                  <li key={message.id} className={cn('max-w-[90%] space-y-2 rounded-lg px-3 py-2 text-sm', message.senderId === viewer?.id ? 'ml-auto bg-primary/10' : 'bg-muted')}>
                    {message.content && <p className="break-words whitespace-pre-wrap">{message.content}</p>}
                    {message.attachments?.map((attachment) => <MediaView key={attachment.mediaId} item={attachment} label="Private chat attachment" />)}
                    {message.createdAt && <time className="block text-xs text-muted-foreground" dateTime={message.createdAt}>{dayjs(message.createdAt).format('HH:mm')}</time>}
                  </li>
                ))}
              </ol>
              {remoteLast === 0 && messages.length === 0 && <EmptyState>No messages yet.</EmptyState>}
              <form className="space-y-2 border-t border-border pt-3" onSubmit={(event) => {
                event.preventDefault()
                setError(null)
                publish.mutate({ data: new FormData(event.currentTarget), form: event.currentTarget })
              }}>
                <label className="grid gap-1.5 text-sm font-medium">Message<Textarea name="message" rows={2} maxLength={10000} /></label>
                <label className="grid gap-1.5 text-sm font-medium">Private attachment<Input name="attachment" type="file" accept={mediaAccept} /></label>
                <RequestError error={publish.error} />
                <Button type="submit" disabled={publish.isPending}>Send message</Button>
              </form>
            </CardContent>
          </Card>
          <aside className="space-y-3">
            <div className="flex flex-wrap gap-1">
              <Button size="sm" variant={sharedTab === 'media' ? 'default' : 'outline'} aria-pressed={sharedTab === 'media'} onClick={() => setSharedTab('media')}>Shared media</Button>
              <Button size="sm" variant={sharedTab === 'links' ? 'default' : 'outline'} aria-pressed={sharedTab === 'links'} onClick={() => setSharedTab('links')}>Shared links</Button>
            </div>
            <RequestError error={shared.error} />
            {sharedTab === 'media' ? (
              <div className="space-y-2">{shared.data?.media.map((item, index) => <MediaView key={`${item.mediaId}:${index}`} item={item} label="Shared chat attachment" />)}</div>
            ) : (
              <ul className="space-y-2">{shared.data?.links.map((link) => <li key={link}><a className="break-all text-sm text-primary hover:underline" href={link} target="_blank" rel="noreferrer">{link}</a></li>)}</ul>
            )}
          </aside>
        </> : <EmptyState>Choose a conversation to read your messages.</EmptyState>}
      </div>
      <Dialog open={compose} onOpenChange={setCompose}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>New conversation</DialogTitle>
            <DialogDescription>Choose the people you want to message.</DialogDescription>
          </DialogHeader>
          <ConversationForm onCreated={(roomId) => {
            setCompose(false)
            void client.invalidateQueries({ queryKey: chatRoomsKey })
            navigate(`/messages/${roomId}`)
          }} />
        </DialogContent>
      </Dialog>
    </Section>
  )
}

function ConversationForm({ onCreated }: { onCreated: (id: string) => void }) {
  const [type, setType] = useState<'DIRECT' | 'GROUP'>('DIRECT')
  const [search, setSearch] = useState('')
  const [selected, setSelected] = useState<string[]>([])
  const contacts = useQuery({
    queryKey: ['contacts', search],
    queryFn: () => apiFetch<ProfileSummary[]>(`/api/users/available-contact?q=${encodeURIComponent(search)}`),
  })
  const create = useMutation({
    mutationFn: (name: FormDataEntryValue | null) => send<{ id: string }>('/api/chat/rooms', 'POST', { type, name, participantIds: selected }),
    onSuccess: (room) => onCreated(room.id),
  })
  return (
    <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); create.mutate(new FormData(event.currentTarget).get('roomName')) }}>
      <label className="grid gap-1 text-sm font-medium">
        Conversation type
        <select className="h-9 rounded-md border border-input bg-background px-2" value={type}
          onChange={(event) => { setType(event.target.value as 'DIRECT' | 'GROUP'); setSelected([]) }}>
          <option value="DIRECT">Direct message</option><option value="GROUP">Group chat</option>
        </select>
      </label>
      {type === 'GROUP' && <label className="grid gap-1 text-sm font-medium">Conversation name<Input name="roomName" required maxLength={100} /></label>}
      <label className="grid gap-1 text-sm font-medium">Find contacts<Input type="search" value={search} onChange={(event) => setSearch(event.target.value)} /></label>
      <div className="max-h-55 space-y-1 overflow-y-auto" aria-label="Available contacts">
        {contacts.data?.map((contact) => (
          <label key={contact.id} className="flex cursor-pointer items-center gap-2 rounded-md p-2 text-sm hover:bg-muted">
            <input type={type === 'DIRECT' ? 'radio' : 'checkbox'} name={type === 'DIRECT' ? 'contact' : undefined}
              checked={selected.includes(contact.id)} onChange={(event) => {
                const checked = event.target.checked
                setSelected((items) => type === 'DIRECT' ? [contact.id] : checked ? [...items, contact.id] : items.filter((id) => id !== contact.id))
              }} />
            <Avatar name={contact.nickName} src={contact.avatarUrl} className="h-7 w-7" />
            {contact.nickName}
          </label>
        ))}
      </div>
      <RequestError error={contacts.error || create.error} />
      <Button type="submit" disabled={selected.length === 0 || create.isPending}>Start conversation</Button>
    </form>
  )
}
