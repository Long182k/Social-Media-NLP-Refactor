import { useQuery } from '@tanstack/react-query'
import { apiFetch } from '@/lib/api'
import type { MediaAttachment } from '@/types/api'

export function MediaView({ item, label }: { item: MediaAttachment; label: string }) {
  const url = useQuery({
    queryKey: ['media', item.mediaId],
    queryFn: () => apiFetch<{ url: string }>(`/api/media/${item.mediaId}/url`),
    staleTime: 55_000,
  })
  if (url.isPending) return <div aria-label="Loading attachment" className="aspect-video rounded-md bg-muted" />
  if (url.isError) return <p className="text-sm text-muted-foreground">Attachment unavailable</p>
  if (item.mimeType.startsWith('video/')) {
    return <video aria-label={label} src={url.data.url} controls preload="metadata" className="max-h-96 w-full rounded-md bg-muted" />
  }
  return <img src={url.data.url} alt={label} loading="lazy" className="max-h-96 max-w-full rounded-md object-contain" />
}
