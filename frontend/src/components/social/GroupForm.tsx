import { useMutation } from '@tanstack/react-query'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { mediaAccept, send, uploadMedia } from '@/lib/socialApi'
import type { Group } from '@/types/api'
import { RequestError } from './Section'

export function GroupForm({ group, onSaved }: { group?: Group; onSaved: (group: Group) => void }) {
  const submit = useMutation({
    mutationFn: async (data: FormData) => {
      const file = data.get('avatar')
      const avatar = file instanceof File && file.size ? await uploadMedia(file, 'PUBLIC') : null
      return send<Group>(group ? `/api/groups/${group.id}` : '/api/groups', group ? 'PATCH' : 'POST', {
        name: data.get('name'),
        description: data.get('description'),
        avatarMediaId: avatar?.mediaId,
      })
    },
    onSuccess: onSaved,
  })
  return (
    <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); submit.mutate(new FormData(event.currentTarget)) }}>
      <label className="grid gap-1.5 text-sm font-medium">
        Group name
        <Input name="name" defaultValue={group?.name} required maxLength={100} />
      </label>
      <label className="grid gap-1.5 text-sm font-medium">
        Description
        <Textarea name="description" defaultValue={group?.description ?? ''} rows={3} maxLength={3000} />
      </label>
      <label className="grid gap-1.5 text-sm font-medium">
        Group image
        <Input name="avatar" type="file" accept={mediaAccept.replace(',video/mp4', '')} />
      </label>
      <RequestError error={submit.error} />
      <Button disabled={submit.isPending} type="submit">{group ? 'Save group' : 'Create group'}</Button>
    </form>
  )
}
