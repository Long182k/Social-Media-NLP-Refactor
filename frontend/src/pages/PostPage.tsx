import { useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { PostCard } from '@/components/feed/PostCard'
import { ConfirmButton } from '@/components/social/ConfirmButton'
import { MediaView } from '@/components/social/MediaView'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { Avatar } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { apiFetch } from '@/lib/api'
import { dayjs } from '@/lib/date'
import { postsApi } from '@/lib/postsApi'
import { mediaAccept, send, uploadMedia } from '@/lib/socialApi'
import { useUIStore } from '@/stores/uiStore'
import type { CommentDTO, CursorPage } from '@/types/api'

export default function PostPage() {
  const { id = '' } = useParams()
  const client = useQueryClient()
  const navigate = useNavigate()
  const viewer = useUIStore((state) => state.user)
  const [editing, setEditing] = useState<string | null>(null)
  const post = useQuery({ queryKey: ['posts', 'detail', id], queryFn: () => postsApi.getPost(id) })
  const comments = useInfiniteQuery({
    queryKey: ['comments', id],
    queryFn: ({ pageParam }) => apiFetch<CursorPage<CommentDTO>>(`/api/comments?postId=${id}&cursor=${encodeURIComponent(pageParam)}`),
    initialPageParam: '',
    getNextPageParam: (page) => page.nextCursor ?? undefined,
    enabled: post.isSuccess,
  })
  const updated = () => Promise.all([
    client.invalidateQueries({ queryKey: ['comments', id] }),
    client.invalidateQueries({ queryKey: ['posts'] }),
  ])
  const submit = useMutation({
    mutationFn: async ({ fields, commentId }: { fields: FormData; commentId?: string; form: HTMLFormElement }) => {
      const file = fields.get('file')
      const image = file instanceof File && file.size ? await uploadMedia(file, 'PUBLIC') : null
      return send<CommentDTO>(commentId ? `/api/comments/${commentId}` : '/api/comments', commentId ? 'PATCH' : 'POST', {
        postId: id, content: fields.get('comment'), mediaIds: image ? [image.mediaId] : undefined,
      })
    },
    onSuccess: (_result, { form, commentId }) => { form.reset(); if (commentId) setEditing(null); return updated() },
  })

  function commentForm(item?: CommentDTO) {
    return (
      <form className="space-y-2" onSubmit={(event) => {
        event.preventDefault()
        submit.mutate({ fields: new FormData(event.currentTarget), form: event.currentTarget, commentId: item?.id })
      }}>
        <label className="grid gap-1.5 text-sm font-medium">
          {item ? 'Edit comment' : 'Write a comment'}
          <Textarea name="comment" rows={2} maxLength={10000} defaultValue={item?.content} />
        </label>
        <label className="grid gap-1.5 text-sm font-medium">
          Comment attachment
          <Input type="file" name="file" accept={mediaAccept} />
        </label>
        <div className="flex gap-2">
          <Button size="sm" type="submit" disabled={submit.isPending}>{item ? 'Save comment' : 'Post comment'}</Button>
          {item && <Button size="sm" variant="ghost" type="button" onClick={() => setEditing(null)}>Cancel</Button>}
        </div>
      </form>
    )
  }
  if (!post.data) return <Section title="Post"><RequestError error={post.error} /></Section>
  const items = comments.data?.pages.flatMap((page) => page.data) ?? []
  return (
    <Section title="Post">
      <PostCard post={post.data} currentUser={viewer}
        onUpdatePost={async (postId, content) => { await postsApi.updatePost(postId, { content }); await updated() }}
        onDeletePost={async (postId) => { await postsApi.deletePost(postId); navigate('/'); await updated() }} />
      <h2 className="text-lg font-semibold">Comments</h2>
      <Card><CardContent className="p-4">{commentForm()}</CardContent></Card>
      <RequestError error={submit.error || comments.error} />
      {comments.isSuccess && items.length === 0 && <EmptyState>No comments yet.</EmptyState>}
      <ul className="space-y-3">
        {items.map((comment) => (
          <li key={comment.id} className="space-y-3 rounded-lg border border-border bg-card p-4">
            <div className="flex items-center gap-2">
              <Avatar name={comment.author.nickName} src={comment.author.avatarUrl} className="h-8 w-8" />
              <div className="mr-auto">
                <Link to={`/profile/${comment.author.id}`} className="block text-sm font-semibold hover:underline">{comment.author.nickName}</Link>
                <span className="text-xs text-muted-foreground">{dayjs(comment.createdAt).fromNow()}{comment.contentVersion > 1 ? ' · Edited' : ''}</span>
              </div>
              <Badge variant="outline">{comment.sentiment}</Badge>
            </div>
            {editing === comment.id ? commentForm(comment) : <p className="whitespace-pre-wrap text-sm">{comment.content}</p>}
            {comment.attachments.map((attachment) => <MediaView key={attachment.mediaId} item={attachment} label="Comment attachment" />)}
            {(viewer?.id === comment.author.id || viewer?.role === 'ADMIN') && <div className="flex flex-wrap gap-2">
              {editing !== comment.id && <Button variant="ghost" size="sm" onClick={() => setEditing(comment.id)}>Edit comment</Button>}
              <ConfirmButton label="Delete comment" description="This comment will be removed." onConfirm={async () => {
                await send(`/api/comments/${comment.id}`, 'DELETE')
                await updated()
              }} />
            </div>}
          </li>
        ))}
      </ul>
      {comments.hasNextPage && <Button variant="outline" onClick={() => void comments.fetchNextPage()}>Load more comments</Button>}
    </Section>
  )
}
