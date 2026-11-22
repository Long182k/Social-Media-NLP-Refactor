import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { Bookmark, Heart, MessageCircle } from 'lucide-react'
import { RequestError } from '@/components/social/Section'
import { Button } from '@/components/ui/button'
import { send } from '@/lib/socialApi'
import type { InteractionState, PostDTO } from '@/types/api'

export function PostActions({ post }: { post: PostDTO }) {
  const client = useQueryClient()
  const action = useMutation({
    mutationFn: (kind: 'like' | 'bookmark') => send<InteractionState>(
      kind === 'like' ? `/api/posts/${post.id}/like` : `/api/bookmarks/${post.id}`, 'POST',
    ),
    onSuccess: () => Promise.all([
      client.invalidateQueries({ queryKey: ['posts'] }),
      client.invalidateQueries({ queryKey: ['bookmarks'] }),
    ]),
  })
  const liked = action.data?.liked ?? post.liked
  const bookmarked = action.data?.bookmarked ?? post.bookmarked
  return (
    <div className="space-y-2 border-t border-border/70 pt-2">
      <div className="flex flex-wrap gap-1">
        <Button size="sm" variant={liked ? 'secondary' : 'ghost'} aria-pressed={!!liked} onClick={() => action.mutate('like')} disabled={action.isPending}>
          <Heart className="h-4 w-4" aria-hidden="true" />
          Like <span>{action.data?.likeCount ?? post.likeCount}</span>
        </Button>
        <Button size="sm" variant={bookmarked ? 'secondary' : 'ghost'} aria-pressed={!!bookmarked} onClick={() => action.mutate('bookmark')} disabled={action.isPending}>
          <Bookmark className="h-4 w-4" aria-hidden="true" />
          {bookmarked ? 'Saved' : 'Bookmark'}
        </Button>
        <Button size="sm" variant="ghost" asChild>
          <Link to={`/posts/${post.id}`}>
            <MessageCircle className="h-4 w-4" aria-hidden="true" />
            Comments <span>{post.commentCount}</span>
          </Link>
        </Button>
      </div>
      <RequestError error={action.error} />
    </div>
  )
}
