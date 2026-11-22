import { useState } from 'react'
import { Link } from 'react-router-dom'
import { Frown, Minus, Pencil, Smile, Trash2 } from 'lucide-react'
import { Avatar } from '@/components/ui/avatar'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader } from '@/components/ui/card'
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Textarea } from '@/components/ui/textarea'
import { MediaView } from '@/components/social/MediaView'
import { PostActions } from './PostActions'
import { dayjs } from '@/lib/date'
import type { PostDTO, PublicUser, Sentiment } from '@/types/api'

interface PostCardProps {
  post: PostDTO
  currentUser: PublicUser | null
  onUpdatePost: (id: string, content: string) => Promise<void>
  onDeletePost: (id: string) => Promise<void>
}

const SENTIMENT_META: Record<
  Sentiment,
  {
    variant: 'good' | 'moderate' | 'bad'
    label: string
    tooltip: string
    Icon: typeof Smile
  }
> = {
  GOOD: {
    variant: 'good',
    label: 'GOOD',
    tooltip: 'This post has a positive sentiment based on its content',
    Icon: Smile,
  },
  MODERATE: {
    variant: 'moderate',
    label: 'MODERATE',
    tooltip: 'This post has a neutral sentiment based on its content',
    Icon: Minus,
  },
  BAD: {
    variant: 'bad',
    label: 'BAD',
    tooltip: 'This post has a negative sentiment based on its content',
    Icon: Frown,
  },
}

export function PostCard({
  post,
  currentUser,
  onUpdatePost,
  onDeletePost,
}: PostCardProps) {
  const [editOpen, setEditOpen] = useState(false)
  const [editContent, setEditContent] = useState(post.content)
  const [editError, setEditError] = useState<string | null>(null)
  const [isSaving, setIsSaving] = useState(false)

  const [deleteOpen, setDeleteOpen] = useState(false)
  const [deleteError, setDeleteError] = useState<string | null>(null)
  const [isDeleting, setIsDeleting] = useState(false)

  const isOwner = currentUser?.id === post.userId
  const canDelete = isOwner || currentUser?.role === 'ADMIN'
  const sentimentInfo = SENTIMENT_META[post.sentiment] ?? SENTIMENT_META.MODERATE
  const SentimentIcon = sentimentInfo.Icon
  const authorDisplay = post.author.nickName || post.author.userName
  const createdIso = new Date(post.createdAt).toISOString()

  function openEditDialog() {
    setEditContent(post.content)
    setEditError(null)
    setEditOpen(true)
  }

  async function handleSaveEdit(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = editContent.trim()
    if (!trimmed && post.attachments.length === 0) {
      setEditError('Post content cannot be empty.')
      return
    }
    setIsSaving(true)
    setEditError(null)
    try {
      await onUpdatePost(post.id, trimmed)
      setEditOpen(false)
    } catch (err) {
      setEditError(err instanceof Error ? err.message : 'Failed to update post.')
    } finally {
      setIsSaving(false)
    }
  }

  async function handleConfirmDelete() {
    setIsDeleting(true)
    setDeleteError(null)
    try {
      await onDeletePost(post.id)
      setDeleteOpen(false)
    } catch (err) {
      setDeleteError(err instanceof Error ? err.message : 'Failed to delete post.')
    } finally {
      setIsDeleting(false)
    }
  }

  return (
    <Card data-testid="post-card" data-post-id={post.id} className="transition-shadow hover:shadow-sm">
      <CardHeader className="flex flex-row items-start justify-between gap-3 space-y-0 pb-3">
        <div className="flex min-w-0 items-center gap-3">
          <Avatar name={authorDisplay} src={post.author.avatarUrl} />
          <div className="min-w-0">
            <div className="flex flex-wrap items-baseline gap-x-2">
              <Link to={`/profile/${post.userId}`} className="truncate text-sm font-semibold text-foreground hover:underline">
                {authorDisplay}
              </Link>
              <span className="truncate text-xs text-muted-foreground">
                @{post.author.userName}
              </span>
            </div>
            <time
              dateTime={createdIso}
              title={createdIso}
              className="text-xs text-muted-foreground"
            >
              {dayjs(post.createdAt).fromNow()}
              {post.contentVersion > 1 ? ' · Edited' : ''}
            </time>
          </div>
        </div>

        <div className="flex shrink-0 items-center gap-2">
          <Badge
            variant={sentimentInfo.variant}
            data-testid="sentiment-badge"
            aria-label={`Sentiment: ${post.sentiment}`}
            title={sentimentInfo.tooltip}
          >
            <SentimentIcon className="h-3.5 w-3.5" aria-hidden="true" />
            <span>{sentimentInfo.label}</span>
          </Badge>
        </div>
      </CardHeader>

      <CardContent className="space-y-3">
        <p
          data-testid="post-content"
          className="whitespace-pre-wrap break-words text-sm leading-relaxed text-foreground"
        >
          {post.content}
        </p>
        {post.attachments.length > 0 && (
          <div className="grid gap-2 sm:grid-cols-2">
            {post.attachments.map((item) => <MediaView key={item.mediaId} item={item} label="Post attachment" />)}
          </div>
        )}
        <PostActions post={post} />

        {(isOwner || canDelete) && (
          <div className="flex items-center justify-end gap-2 border-t border-border/70 pt-3">
            {isOwner && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={openEditDialog}
                aria-label="Edit post"
              >
                <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                <span>Edit</span>
              </Button>
            )}
            {canDelete && (
              <Button
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => {
                  setDeleteError(null)
                  setDeleteOpen(true)
                }}
                className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                aria-label="Delete post"
              >
                <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                <span>Delete</span>
              </Button>
            )}
          </div>
        )}
      </CardContent>

      <Dialog open={editOpen} onOpenChange={setEditOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Edit post</DialogTitle>
            <DialogDescription>
              Updating your post will recompute its sentiment label automatically.
            </DialogDescription>
          </DialogHeader>
          <form onSubmit={handleSaveEdit} className="mt-4 space-y-4">
            <div className="space-y-1.5">
              <label htmlFor={`edit-post-${post.id}`} className="text-sm font-medium">
                Post content
              </label>
              <Textarea
                id={`edit-post-${post.id}`}
                value={editContent}
                onChange={(e) => setEditContent(e.target.value)}
                rows={4}
                maxLength={10000}
                disabled={isSaving}
              />
            </div>
            {editError && (
              <p role="alert" className="text-sm text-destructive">
                {editError}
              </p>
            )}
            <DialogFooter>
              <Button
                type="button"
                variant="outline"
                onClick={() => setEditOpen(false)}
                disabled={isSaving}
              >
                Cancel
              </Button>
              <Button type="submit" disabled={isSaving}>
                {isSaving ? 'Saving...' : 'Save changes'}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>

      <Dialog open={deleteOpen} onOpenChange={setDeleteOpen}>
        <DialogContent>
          <DialogHeader>
            <DialogTitle>Delete post</DialogTitle>
            <DialogDescription>
              Are you sure you want to delete this post? This action cannot be undone.
            </DialogDescription>
          </DialogHeader>
          {deleteError && (
            <p role="alert" className="mt-3 text-sm text-destructive">
              {deleteError}
            </p>
          )}
          <DialogFooter>
            <Button
              type="button"
              variant="outline"
              onClick={() => setDeleteOpen(false)}
              disabled={isDeleting}
            >
              Cancel
            </Button>
            <Button
              type="button"
              variant="destructive"
              onClick={handleConfirmDelete}
              disabled={isDeleting}
            >
              {isDeleting ? 'Deleting...' : 'Confirm delete'}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </Card>
  )
}
