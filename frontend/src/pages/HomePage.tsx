import { useEffect, useMemo, useRef, useState } from 'react'
import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Send } from 'lucide-react'
import { PostCard } from '@/components/feed/PostCard'
import { useAppLayoutContext } from '@/components/layout/layoutContext'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { postsApi } from '@/lib/postsApi'
import { mediaAccept, uploadMedia } from '@/lib/socialApi'
import { useUIStore } from '@/stores/uiStore'
import type { PostDTO } from '@/types/api'

export default function HomePage() {
  const queryClient = useQueryClient()
  const user = useUIStore((s) => s.user)
  const { searchQuery } = useAppLayoutContext()

  const [draft, setDraft] = useState('')
  const [composerError, setComposerError] = useState<string | null>(null)
  const sentinelRef = useRef<HTMLDivElement | null>(null)
  const attachmentRef = useRef<HTMLInputElement>(null)

  const feedQuery = useInfiniteQuery({
    queryKey: ['posts', searchQuery],
    initialPageParam: undefined as string | undefined,
    queryFn: ({ pageParam }) =>
      postsApi.listPosts({
        cursor: pageParam,
        limit: 10,
        q: searchQuery || undefined,
      }),
    getNextPageParam: (lastPage) =>
      lastPage.hasMore && lastPage.nextCursor ? lastPage.nextCursor : undefined,
  })

  const { hasNextPage, isFetchingNextPage, isFetchNextPageError, fetchNextPage } = feedQuery

  useEffect(() => {
    const el = sentinelRef.current
    if (!el || !hasNextPage || typeof IntersectionObserver === 'undefined') {
      return
    }
    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[0]
        if (entry?.isIntersecting && hasNextPage && !isFetchingNextPage && !isFetchNextPageError) {
          void fetchNextPage()
        }
      },
      { rootMargin: '200px 0px' },
    )
    observer.observe(el)
    return () => observer.disconnect()
  }, [hasNextPage, isFetchingNextPage, isFetchNextPageError, fetchNextPage])

  const createMutation = useMutation({
    mutationFn: async ({ content, file }: { content: string; file?: File }) => {
      const attachment = file ? await uploadMedia(file, 'PUBLIC') : null
      return postsApi.createPost({ content, mediaIds: attachment ? [attachment.mediaId] : undefined })
    },
    onSuccess: async () => {
      setDraft('')
      if (attachmentRef.current) attachmentRef.current.value = ''
      setComposerError(null)
      await queryClient.invalidateQueries({ queryKey: ['posts'] })
    },
    onError: (err) => {
      setComposerError(err instanceof Error ? err.message : 'Failed to publish post.')
    },
  })

  const updateMutation = useMutation({
    mutationFn: ({ id, content }: { id: string; content: string }) =>
      postsApi.updatePost(id, { content }),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['posts'] })
    },
  })

  const deleteMutation = useMutation({
    mutationFn: (id: string) => postsApi.deletePost(id),
    onSuccess: async () => {
      await queryClient.invalidateQueries({ queryKey: ['posts'] })
    },
  })

  const posts = useMemo(() => {
    const seen = new Set<string>()
    const unique: PostDTO[] = []
    for (const page of feedQuery.data?.pages ?? []) {
      for (const post of page.data) {
        if (!seen.has(post.id)) {
          seen.add(post.id)
          unique.push(post)
        }
      }
    }
    return unique
  }, [feedQuery.data])

  function handleCreatePost(e: React.FormEvent) {
    e.preventDefault()
    const trimmed = draft.trim()
    const file = attachmentRef.current?.files?.[0]
    if (!trimmed && !file) {
      setComposerError('Write an update or choose an attachment.')
      return
    }
    setComposerError(null)
    createMutation.mutate({ content: trimmed, file })
  }

  return (
    <div className="space-y-6">
      <section aria-label="Create post">
        <Card>
          <CardContent className="p-5">
            <form onSubmit={handleCreatePost} className="space-y-3">
              <label htmlFor="post-composer" className="block text-sm font-semibold">
                Share an update
              </label>
              <Textarea
                id="post-composer"
                placeholder={`What's on your mind, ${user?.nickName || user?.userName || 'friend'}?`}
                value={draft}
                onChange={(e) => setDraft(e.target.value)}
                rows={3}
                maxLength={10000}
                disabled={createMutation.isPending}
              />
              <label className="grid gap-1.5 text-sm font-medium">
                Attachment
                <Input type="file" ref={attachmentRef} accept={mediaAccept} disabled={createMutation.isPending} />
              </label>

              {composerError && (
                <p role="alert" className="text-sm text-destructive">
                  {composerError}
                </p>
              )}

              <div className="flex items-center justify-between gap-3">
                <p className="text-xs text-muted-foreground">
                  Automatic Vietnamese &amp; English sentiment scoring on publish.
                </p>
                <Button type="submit" disabled={createMutation.isPending}>
                  <Send className="h-4 w-4" aria-hidden="true" />
                  <span>{createMutation.isPending ? 'Posting...' : 'Post'}</span>
                </Button>
              </div>
            </form>
          </CardContent>
        </Card>
      </section>

      <section aria-label="Feed" className="space-y-4">
        {searchQuery && (
          <p className="text-sm text-muted-foreground">
            Showing posts matching <span className="font-semibold text-foreground">&ldquo;{searchQuery}&rdquo;</span>
          </p>
        )}

        {feedQuery.isLoading ? (
          <div className="space-y-3" role="status" aria-live="polite">
            {[1, 2, 3].map((n) => (
              <Card key={n} className="p-5">
                <div className="space-y-3 animate-pulse">
                  <div className="flex items-center gap-3">
                    <div className="h-10 w-10 rounded-full bg-muted" />
                    <div className="space-y-1.5">
                      <div className="h-3.5 w-32 rounded bg-muted" />
                      <div className="h-3 w-20 rounded bg-muted" />
                    </div>
                  </div>
                  <div className="h-4 w-full rounded bg-muted" />
                  <div className="h-4 w-3/4 rounded bg-muted" />
                </div>
              </Card>
            ))}
          </div>
        ) : feedQuery.isError ? (
          <Card>
            <CardContent className="flex flex-col items-center gap-3 p-6 text-center">
              <p className="text-sm text-destructive">
                {feedQuery.error instanceof Error
                  ? feedQuery.error.message
                  : 'Unable to load posts.'}
              </p>
              <Button variant="outline" size="sm" onClick={() => feedQuery.refetch()}>
                Try again
              </Button>
            </CardContent>
          </Card>
        ) : posts.length === 0 ? (
          <Card>
            <CardContent className="p-8 text-center text-sm text-muted-foreground">
              {searchQuery
                ? 'No posts matched your search query.'
                : 'No posts yet. Be the first to share something!'}
            </CardContent>
          </Card>
        ) : (
          <>
            <div className="space-y-4">
              {posts.map((post) => (
                <PostCard
                  key={post.id}
                  post={post}
                  currentUser={user}
                  onUpdatePost={async (id, content) => {
                    await updateMutation.mutateAsync({ id, content })
                  }}
                  onDeletePost={async (id) => {
                    await deleteMutation.mutateAsync(id)
                  }}
                />
              ))}
            </div>

            {feedQuery.hasNextPage && (
              <div ref={sentinelRef} data-testid="feed-scroll-sentinel" className="flex justify-center pt-2">
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => feedQuery.fetchNextPage()}
                  disabled={feedQuery.isFetchingNextPage}
                >
                  {feedQuery.isFetchingNextPage
                    ? 'Loading more...'
                    : feedQuery.isFetchNextPageError
                      ? 'Retry loading more'
                      : 'Load more'}
                </Button>
              </div>
            )}
          </>
        )}
      </section>
    </div>
  )
}
