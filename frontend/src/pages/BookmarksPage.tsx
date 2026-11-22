import { useInfiniteQuery, useMutation, useQueryClient } from '@tanstack/react-query'
import { Link } from 'react-router-dom'
import { BookMarked } from 'lucide-react'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { apiFetch } from '@/lib/api'
import { send } from '@/lib/socialApi'
import type { Bookmark, CursorPage } from '@/types/api'

export default function BookmarksPage() {
  const client = useQueryClient()
  const saved = useInfiniteQuery({
    queryKey: ['bookmarks'],
    initialPageParam: '',
    queryFn: ({ pageParam }) => apiFetch<CursorPage<Bookmark>>(`/api/bookmarks?cursor=${encodeURIComponent(pageParam)}`),
    getNextPageParam: (page) => page.nextCursor ?? undefined,
  })
  const remove = useMutation({
    mutationFn: (id: string) => send<void>(`/api/bookmarks/${id}`, 'DELETE'),
    onSuccess: () => Promise.all([
      client.invalidateQueries({ queryKey: ['bookmarks'] }),
      client.invalidateQueries({ queryKey: ['posts'] }),
    ]),
  })
  const items = saved.data?.pages.flatMap((page) => page.data) ?? []
  return (
    <Section title="Bookmarks">
      <RequestError error={saved.error || remove.error} />
      {saved.isSuccess && items.length === 0 && <EmptyState>Saved posts appear here.</EmptyState>}
      <div className="space-y-3">
        {items.map((item) => <Card key={item.id}>
          <CardContent className="flex flex-wrap items-start gap-3 p-4">
            <BookMarked className="mt-1 h-5 w-5 text-primary" aria-hidden="true" />
            <div className="min-w-0 flex-1 space-y-1">
              <Link to={`/posts/${item.postId}`} className="line-clamp-3 whitespace-pre-wrap font-medium hover:underline">{item.content || 'View saved attachment'}</Link>
              <p className="text-xs text-muted-foreground">{item.authorName}</p>
            </div>
            <Button variant="outline" size="sm" disabled={remove.isPending} onClick={() => remove.mutate(item.id)}>Remove bookmark</Button>
          </CardContent>
        </Card>)}
      </div>
      {saved.hasNextPage && <Button variant="outline" onClick={() => void saved.fetchNextPage()}>Load more bookmarks</Button>}
    </Section>
  )
}
