import { useState } from 'react'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ConfirmButton } from '@/components/social/ConfirmButton'
import { EventForm } from '@/components/social/EventForm'
import { MediaView } from '@/components/social/MediaView'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { Avatar } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { apiFetch } from '@/lib/api'
import { dayjs } from '@/lib/date'
import { send } from '@/lib/socialApi'
import { useUIStore } from '@/stores/uiStore'
import type { CommunityEvent, ProfileSummary } from '@/types/api'

export default function EventPage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const client = useQueryClient()
  const user = useUIStore((state) => state.user)
  const [editing, setEditing] = useState(false)
  const path = `/api/events/${id}`
  const event = useQuery({ queryKey: ['events', id], queryFn: () => apiFetch<CommunityEvent>(path) })
  const manager = !!(user && event.data && (user.id === event.data.creatorId || user.role === 'ADMIN'))
  const attendees = useQuery({ queryKey: ['events', id, 'attendees'], queryFn: () => apiFetch<ProfileSummary[]>(`${path}/attendees`), enabled: event.isSuccess })
  const requests = useQuery({ queryKey: ['events', id, 'requests'], queryFn: () => apiFetch<ProfileSummary[]>(`${path}/requests`), enabled: manager })
  const rsvp = useMutation({
    mutationFn: (suffix: string) => send<void>(`${path}${suffix}`, 'POST'),
    onSuccess: () => client.invalidateQueries({ queryKey: ['events'] }),
  })
  if (!event.data) return <Section title="Event"><RequestError error={event.error} /></Section>
  const item = event.data
  const attending = item.viewerStatus === 'ENROLL'
  const pending = item.viewerStatus === 'PENDING'
  return (
    <Section title={item.name}>
      <Link to="/events" className="text-sm text-primary hover:underline">All events</Link>
      {item.avatarMediaId && <MediaView item={{ mediaId: item.avatarMediaId, mimeType: 'image/png' }} label={item.name} />}
      <Card><CardContent className="space-y-3 p-4">
        <p className="text-sm font-medium text-primary">{dayjs(item.eventDate).format('ddd, MMM D, YYYY · HH:mm')} · {item.category}</p>
        {item.address && <p className="text-sm text-muted-foreground">{item.address}</p>}
        <p className="whitespace-pre-wrap text-sm">{item.description}</p>
        <div className="flex flex-wrap gap-2">
          {user?.id !== item.creatorId && (attending || pending ? (
            <Button variant="outline" disabled={rsvp.isPending} onClick={() => rsvp.mutate(`/cancel/${user?.id}`)}>
              {pending ? 'Cancel RSVP request' : 'Cancel RSVP'}
            </Button>
          ) : <Button disabled={rsvp.isPending} onClick={() => rsvp.mutate('/join')}>RSVP</Button>)}
          {manager && <>
            <Button variant="outline" onClick={() => setEditing(true)}>Edit event</Button>
            <ConfirmButton label="Delete event" description="The event and its RSVPs will be removed." onConfirm={async () => {
              await send(path, 'DELETE')
              await client.invalidateQueries({ queryKey: ['events'] })
              navigate('/events')
            }} />
          </>}
        </div>
        {pending && <p role="status" className="text-sm text-muted-foreground">RSVP request pending</p>}
        <RequestError error={rsvp.error} />
      </CardContent></Card>
      {manager && requests.data && requests.data.length > 0 && (
        <div className="space-y-2">
          <h2 className="text-lg font-semibold">RSVP requests</h2>
          {requests.data.map((person) => (
            <div className="flex flex-wrap items-center gap-2 rounded-lg border border-border p-3" key={person.id}>
              <span className="mr-auto">{person.nickName}</span>
              <Button size="sm" disabled={rsvp.isPending} onClick={() => rsvp.mutate(`/approve/${person.id}`)}>Approve RSVP</Button>
              <Button size="sm" variant="outline" disabled={rsvp.isPending} onClick={() => rsvp.mutate(`/cancel/${person.id}`)}>Decline</Button>
            </div>
          ))}
        </div>
      )}
      <h2 className="text-lg font-semibold">Attending ({item.attendeeCount})</h2>
      <RequestError error={attendees.error || requests.error} />
      {attendees.isSuccess && attendees.data.length === 0 && <EmptyState>No attendees yet.</EmptyState>}
      <ul className="grid gap-2 sm:grid-cols-2">
        {attendees.data?.map((person) => (
          <li key={person.id} className="flex items-center gap-2 rounded-lg border border-border p-3">
            <Avatar name={person.nickName} src={person.avatarUrl} />
            <Link className="text-sm font-medium hover:underline" to={`/profile/${person.id}`}>{person.nickName}</Link>
          </li>
        ))}
      </ul>
      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent>
          <DialogHeader><DialogTitle>Edit event</DialogTitle><DialogDescription>Update the schedule or event details.</DialogDescription></DialogHeader>
          <EventForm event={item} onSaved={() => { setEditing(false); void client.invalidateQueries({ queryKey: ['events'] }) }} />
        </DialogContent>
      </Dialog>
    </Section>
  )
}
