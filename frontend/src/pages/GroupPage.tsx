import { useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { ConfirmButton } from '@/components/social/ConfirmButton'
import { GroupForm } from '@/components/social/GroupForm'
import { MediaView } from '@/components/social/MediaView'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { PostCard } from '@/components/feed/PostCard'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { apiFetch } from '@/lib/api'
import { readGroup, readScopedFeed } from '@/lib/graphApi'
import { postsApi } from '@/lib/postsApi'
import { mediaAccept, send, uploadMedia } from '@/lib/socialApi'
import { useUIStore } from '@/stores/uiStore'
import type { PostDTO, ProfileSummary } from '@/types/api'

export default function GroupPage() {
  const { id = '' } = useParams()
  const path = `/api/groups/${id}`
  const navigate = useNavigate()
  const client = useQueryClient()
  const user = useUIStore((state) => state.user)
  const [editing, setEditing] = useState(false)
  const [copyMessage, setCopyMessage] = useState('')
  const group = useQuery({ queryKey: ['groups', id], queryFn: () => readGroup(id) })
  const manager = !!(user && group.data && (user.id === group.data.creatorId || user.role === 'ADMIN' || group.data.viewerRole === 'ADMIN'))
  const requests = useQuery({
    queryKey: ['groups', id, 'requests'],
    queryFn: () => apiFetch<ProfileSummary[]>(`${path}/join-requests`),
    enabled: manager,
  })
  const feed = useInfiniteQuery({
    queryKey: ['posts', 'group', id],
    queryFn: ({ pageParam }) => readScopedFeed('groupDetail', id, pageParam),
    initialPageParam: '',
    getNextPageParam: (page) => page.nextCursor ?? undefined,
    enabled: group.isSuccess,
  })
  const membership = useMutation({
    mutationFn: ({ suffix, method }: { suffix: string; method: 'POST' | 'DELETE' }) => send<void>(`${path}${suffix}`, method),
    onSuccess: () => client.invalidateQueries({ queryKey: ['groups'] }),
  })
  const publish = useMutation({
    mutationFn: async ({ data }: { data: FormData; form: HTMLFormElement }) => {
      const file = data.get('attachment')
      const attachment = file instanceof File && file.size ? await uploadMedia(file, 'PUBLIC') : null
      return send<PostDTO>(`${path}/posts`, 'POST', { content: data.get('content'), mediaIds: attachment ? [attachment.mediaId] : [] })
    },
    onSuccess: (_post, { form }) => { form.reset(); return client.invalidateQueries({ queryKey: ['posts', 'group', id] }) },
  })
  function invalidate() {
    return client.invalidateQueries({ queryKey: ['posts'] })
  }

  async function copyLink() {
    try {
      await navigator.clipboard.writeText(window.location.href)
      setCopyMessage('Group link copied.')
    } catch {
      setCopyMessage('Copy the group link from the address bar.')
    }
  }

  if (!group.data) return <Section title="Group"><RequestError error={group.error} /></Section>
  const detail = group.data
  const joined = detail.viewerStatus === 'JOINED'
  const pending = detail.viewerStatus === 'PENDING'
  const posts = feed.data?.pages.flatMap((page) => page.data) ?? []
  return (
    <Section title={detail.name} action={<Button variant="outline" onClick={() => void copyLink()}>Copy group link</Button>}>
      <Link className="text-sm text-primary hover:underline" to="/groups">All groups</Link>
      {copyMessage && <p role="status" className="text-sm text-muted-foreground">{copyMessage}</p>}
      {detail.avatarMediaId && <MediaView item={{ mediaId: detail.avatarMediaId, mimeType: 'image/png' }} label={detail.name} />}
      {detail.description && <p className="whitespace-pre-wrap text-sm text-muted-foreground">{detail.description}</p>}
      <div className="flex flex-wrap gap-2">
        {joined || pending ? (
          <Button variant="outline" disabled={membership.isPending} onClick={() => membership.mutate({ suffix: '/membership', method: 'DELETE' })}>
            {joined ? 'Leave group' : 'Cancel join request'}
          </Button>
        ) : (
          <Button disabled={membership.isPending} onClick={() => membership.mutate({ suffix: '/join', method: 'POST' })}>Join group</Button>
        )}
        {manager && <>
          <Button variant="outline" onClick={() => setEditing(true)}>Edit group</Button>
          <ConfirmButton label="Delete group" description="The group and its posts will be removed." onConfirm={async () => {
            await send(path, 'DELETE')
            await client.invalidateQueries({ queryKey: ['groups'] })
            navigate('/groups')
          }} />
        </>}
      </div>
      <RequestError error={membership.error} />
      {manager && requests.data && requests.data.length > 0 && (
        <Card><CardContent className="space-y-3 p-4">
          <h2 className="font-semibold">Join requests</h2>
          {requests.data.map((request) => (
            <div key={request.id} className="flex flex-wrap items-center gap-2 rounded-md border border-border p-2">
              <span className="mr-auto">{request.nickName}</span>
              <Button size="sm" disabled={membership.isPending} onClick={() => membership.mutate({ suffix: `/approve/${request.id}`, method: 'POST' })}>Approve</Button>
              <Button size="sm" variant="outline" disabled={membership.isPending} onClick={() => membership.mutate({ suffix: `/reject/${request.id}`, method: 'POST' })}>Reject</Button>
            </div>
          ))}
        </CardContent></Card>
      )}
      {joined && <Card><CardContent className="p-4">
        <form className="space-y-3" onSubmit={(event) => {
          event.preventDefault()
          publish.mutate({ data: new FormData(event.currentTarget), form: event.currentTarget })
        }}>
          <label className="grid gap-1.5 text-sm font-medium">Group post<Textarea name="content" rows={3} maxLength={10000} /></label>
          <label className="grid gap-1.5 text-sm font-medium">Attachment<Input type="file" name="attachment" accept={mediaAccept} /></label>
          <RequestError error={publish.error} />
          <Button disabled={publish.isPending} type="submit">Post to group</Button>
        </form>
      </CardContent></Card>}
      <h2 className="text-lg font-semibold">Group posts</h2>
      <RequestError error={feed.error} />
      {feed.isSuccess && posts.length === 0 && <EmptyState>No group posts yet.</EmptyState>}
      {posts.map((post) => (
        <PostCard key={post.id} post={post} currentUser={user}
          onUpdatePost={async (postId, content) => { await postsApi.updatePost(postId, { content }); await invalidate() }}
          onDeletePost={async (postId) => { await postsApi.deletePost(postId); await invalidate() }} />
      ))}
      {feed.hasNextPage && <Button variant="outline" onClick={() => void feed.fetchNextPage()}>Load more posts</Button>}
      <Dialog open={editing} onOpenChange={setEditing}>
        <DialogContent>
          <DialogHeader><DialogTitle>Edit group</DialogTitle><DialogDescription>Update your group details.</DialogDescription></DialogHeader>
          <GroupForm group={detail} onSaved={() => { setEditing(false); void client.invalidateQueries({ queryKey: ['groups'] }) }} />
        </DialogContent>
      </Dialog>
    </Section>
  )
}
