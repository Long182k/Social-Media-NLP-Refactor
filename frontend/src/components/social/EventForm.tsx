import { useMutation } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { mediaAccept, send, uploadMedia } from '@/lib/socialApi'
import { dayjs } from '@/lib/date'
import type { CommunityEvent } from '@/types/api'
import { RequestError } from './Section'

export function EventForm({ event, onSaved }: { event?: CommunityEvent; onSaved: (event: CommunityEvent) => void }) {
  const submit = useMutation({
    mutationFn: async (data: FormData) => {
      const file = data.get('avatar')
      const avatar = file instanceof File && file.size ? await uploadMedia(file, 'PUBLIC') : null
      const eventDate = new Date(String(data.get('eventDate')))
      return send<CommunityEvent>(event ? `/api/events/${event.id}` : '/api/events', event ? 'PATCH' : 'POST', {
        name: data.get('name'), description: data.get('description'), address: data.get('address'),
        category: data.get('category'), avatarMediaId: avatar?.mediaId, eventDate: eventDate.toISOString(),
      })
    },
    onSuccess: onSaved,
  })
  return (
    <form className="space-y-3" onSubmit={(e) => { e.preventDefault(); submit.mutate(new FormData(e.currentTarget)) }}>
      <label className="grid gap-1.5 text-sm font-medium">Event name<Input name="name" defaultValue={event?.name} required maxLength={100} /></label>
      <label className="grid gap-1.5 text-sm font-medium">Description<Textarea name="description" defaultValue={event?.description} rows={3} required maxLength={3000} /></label>
      <div className="grid gap-3 sm:grid-cols-2">
        <label className="grid gap-1.5 text-sm font-medium">Date and time<Input name="eventDate" type="datetime-local" defaultValue={event ? dayjs(event.eventDate).format('YYYY-MM-DDTHH:mm') : undefined} required /></label>
        <label className="grid gap-1.5 text-sm font-medium">
          Category
          <select name="category" aria-label="Category" className="h-9 rounded-md border border-input bg-background px-2" defaultValue={event?.category ?? 'OTHER'}>
            {['MUSIC', 'SPORTS', 'EDUCATION', 'TECHNOLOGY', 'FOOD', 'ART', 'BUSINESS', 'HEALTH', 'OTHER'].map((category) => <option key={category}>{category}</option>)}
          </select>
        </label>
      </div>
      <label className="grid gap-1.5 text-sm font-medium">Location<Input name="address" defaultValue={event?.address ?? ''} maxLength={300} /></label>
      <label className="grid gap-1.5 text-sm font-medium">Event image<Input name="avatar" type="file" accept={mediaAccept.replace(',video/mp4', '')} /></label>
      <RequestError error={submit.error} />
      <Button disabled={submit.isPending} type="submit">{event ? 'Save event' : 'Create event'}</Button>
    </form>
  )
}
