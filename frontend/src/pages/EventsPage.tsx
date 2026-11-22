import { useState } from 'react'
import { useInfiniteQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { EventForm } from '@/components/social/EventForm'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { apiFetch } from '@/lib/api'
import { dayjs } from '@/lib/date'
import type { CommunityEvent } from '@/types/api'

const modes = [
  { label: 'All events', path: '/api/events' },
  { label: 'Discover', path: '/api/events/all/discover' },
  { label: 'My events', path: '/api/events/all/my-events' },
  { label: 'Trending', path: '/api/events/trending/top' },
]

export default function EventsPage() {
  const navigate = useNavigate()
  const client = useQueryClient()
  const [mode, setMode] = useState(modes[0])
  const [category, setCategory] = useState('')
  const [search, setSearch] = useState('')
  const [creating, setCreating] = useState(false)
  const path = category ? `/api/events/category/${category}` : mode.path
  const events = useInfiniteQuery({
    queryKey: ['events', path, search],
    queryFn: ({ pageParam }) => apiFetch<CommunityEvent[]>(`${path}?${new URLSearchParams({ q: search, offset: String(pageParam) })}`),
    initialPageParam: 0,
    getNextPageParam: (page, _pages, offset) => page.length === 50 ? offset + 50 : undefined,
  })
  const items = events.data?.pages.flat() ?? []
  return (
    <Section title="Events" action={<Button onClick={() => setCreating(true)}>New event</Button>}>
      <div className="flex flex-wrap gap-2">
        {modes.map((item) => (
          <Button key={item.path} variant={mode === item && !category ? 'default' : 'outline'} aria-pressed={mode === item && !category} onClick={() => { setCategory(''); setMode(item) }}>
            {item.label}
          </Button>
        ))}
      </div>
      <div className="flex flex-wrap gap-2">
        <Input type="search" aria-label="Search events" className="sm:max-w-xs" value={search} onChange={(event) => setSearch(event.target.value)} />
        <select aria-label="Event category" value={category} className="h-9 rounded-md border border-input bg-background px-2 text-sm" onChange={(event) => setCategory(event.target.value)}>
          <option value="">All categories</option>
          {['MUSIC', 'SPORTS', 'EDUCATION', 'TECHNOLOGY', 'FOOD', 'ART', 'BUSINESS', 'HEALTH', 'OTHER'].map((item) => <option key={item}>{item}</option>)}
        </select>
      </div>
      <RequestError error={events.error} />
      {events.isSuccess && items.length === 0 && <EmptyState>No events match these filters.</EmptyState>}
      <div className="grid gap-4 sm:grid-cols-2">
        {items.map((event) => (
          <Card key={event.id}>
            <CardContent className="space-y-2 p-4">
              <p className="text-xs font-medium text-primary">{dayjs(event.eventDate).format('ddd, MMM D · HH:mm')}</p>
              <Link to={`/events/${event.id}`} className="block font-semibold hover:underline">{event.name}</Link>
              <p className="line-clamp-2 text-sm text-muted-foreground">{event.description}</p>
              <div className="flex items-center justify-between gap-2"><Badge variant="outline">{event.category}</Badge><span className="text-xs text-muted-foreground">{event.attendeeCount} attending</span></div>
            </CardContent>
          </Card>
        ))}
      </div>
      {events.hasNextPage && <Button variant="outline" onClick={() => void events.fetchNextPage()}>Load more events</Button>}
      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent>
          <DialogHeader><DialogTitle>New event</DialogTitle><DialogDescription>Set the time and details for your event.</DialogDescription></DialogHeader>
          <EventForm onSaved={(event) => { setCreating(false); void client.invalidateQueries({ queryKey: ['events'] }); navigate(`/events/${event.id}`) }} />
        </DialogContent>
      </Dialog>
    </Section>
  )
}
