import { useState } from 'react'
import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate, useParams } from 'react-router-dom'
import { PostCard } from '@/components/feed/PostCard'
import { MediaView } from '@/components/social/MediaView'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { Avatar } from '@/components/ui/avatar'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { apiFetch } from '@/lib/api'
import { readProfile, readScopedFeed } from '@/lib/graphApi'
import { postsApi } from '@/lib/postsApi'
import { chatRoomsKey, send } from '@/lib/socialApi'
import { useUIStore } from '@/stores/uiStore'
import type { MediaAttachment, ProfileSummary } from '@/types/api'

function PeopleList({ people }: { people: ProfileSummary[] }) {
  return (
    <ul className="space-y-2">
      {people.map((person) => (
        <li key={person.id}>
          <Link to={`/profile/${person.id}`} className="flex items-center gap-2 rounded-md p-2 text-sm hover:bg-muted focus-visible:ring-2 focus-visible:ring-ring">
            <Avatar name={person.nickName} src={person.avatarUrl} className="h-8 w-8" />
            <span className="min-w-0 truncate">{person.nickName}</span>
          </Link>
        </li>
      ))}
    </ul>
  )
}

export default function ProfilePage() {
  const { id = '' } = useParams()
  const navigate = useNavigate()
  const client = useQueryClient()
  const user = useUIStore((state) => state.user)
  const [tab, setTab] = useState<'posts' | 'photos' | 'videos'>('posts')
  const [relationship, setRelationship] = useState<'followers' | 'following' | null>(null)
  const [keyword, setKeyword] = useState('')
  const [search, setSearch] = useState('')
  const path = `/api/users/${id}`
  const own = user?.id === id
  const profile = useQuery({ queryKey: ['profile', id], queryFn: () => readProfile(id) })
  const feed = useInfiniteQuery({
    queryKey: ['posts', 'profile', id],
    queryFn: ({ pageParam }) => readScopedFeed('userProfile', id, pageParam),
    initialPageParam: '',
    getNextPageParam: (page) => page.nextCursor ?? undefined,
    enabled: tab === 'posts' && profile.isSuccess,
  })
  const media = useInfiniteQuery({
    queryKey: ['profile', id, tab, 'media'],
    queryFn: ({ pageParam }) => apiFetch<MediaAttachment[]>(`${path}/${tab}?offset=${pageParam}`),
    initialPageParam: 0,
    getNextPageParam: (page, _all, offset) => page.length === 50 ? offset + 50 : undefined,
    enabled: tab !== 'posts' && profile.isSuccess,
  })
  const connections = useQuery({
    queryKey: ['profile', id, relationship],
    queryFn: () => apiFetch<ProfileSummary[]>(`${path}/${relationship}`),
    enabled: !!relationship,
  })
  const suggested = useQuery({ queryKey: ['people', 'suggestions'], queryFn: () => apiFetch<ProfileSummary[]>('/api/users/suggestions') })
  const birthdays = useQuery({ queryKey: ['people', 'birthdays'], queryFn: () => apiFetch<ProfileSummary[]>('/api/users/recent_birthday?upcoming=true') })
  const people = useQuery({
    queryKey: ['people', 'search', search],
    queryFn: () => apiFetch<ProfileSummary[]>(`/api/users?keyword=${encodeURIComponent(search)}`),
    enabled: !!search,
  })
  const follow = useMutation({
    mutationFn: () => send(`${path}/follow`, 'POST'),
    onSuccess: () => Promise.all([
      client.invalidateQueries({ queryKey: ['profile'] }),
      client.invalidateQueries({ queryKey: ['people'] }),
    ]),
  })
  const message = useMutation({
    mutationFn: () => send<{ id: string }>('/api/chat/rooms', 'POST', { type: 'DIRECT', participantIds: [id] }),
    onSuccess: (room) => { void client.invalidateQueries({ queryKey: chatRoomsKey }); navigate(`/messages/${room.id}`) },
  })

  if (!profile.data) return <Section title="Profile"><RequestError error={profile.error} /></Section>
  const member = profile.data
  const posts = feed.data?.pages.flatMap((page) => page.data) ?? []
  const assets = media.data?.pages.flat() ?? []
  return (
    <Section title={member.nickName} action={own ? <Button asChild variant="outline"><Link to="/settings">Edit profile</Link></Button> : undefined}>
      <Card className="overflow-hidden">
        {member.coverPageUrl && <img src={member.coverPageUrl} alt="Profile cover" className="h-36 w-full object-cover sm:h-52" />}
        <CardContent className="space-y-3 p-4">
          <div className="flex flex-wrap items-center gap-3">
            <Avatar name={member.nickName} src={member.avatarUrl} className="h-12 w-12" />
            <span className="mr-auto text-sm text-muted-foreground">@{member.userName}</span>
            {!own && <>
              <Button variant={member.following ? 'outline' : 'default'} aria-pressed={member.following} disabled={follow.isPending} onClick={() => follow.mutate()}>
                {member.following ? 'Unfollow' : 'Follow'}
              </Button>
              <Button variant="outline" disabled={message.isPending} onClick={() => message.mutate()}>Message</Button>
            </>}
          </div>
          {member.bio && <p className="whitespace-pre-wrap text-sm">{member.bio}</p>}
          <div className="flex flex-wrap gap-2">
            <Button variant="ghost" size="sm" aria-pressed={relationship === 'followers'} onClick={() => setRelationship(relationship === 'followers' ? null : 'followers')}>{member.followerCount} followers</Button>
            <Button variant="ghost" size="sm" aria-pressed={relationship === 'following'} onClick={() => setRelationship(relationship === 'following' ? null : 'following')}>{member.followingCount} following</Button>
          </div>
          <RequestError error={follow.error || message.error || connections.error} />
          {relationship && connections.data && <PeopleList people={connections.data} />}
        </CardContent>
      </Card>
      <div className="grid items-start gap-5 lg:grid-cols-[minmax(0,1fr)_minmax(240px,1fr)]">
        <div className="space-y-4">
          <div className="flex flex-wrap gap-2">
            {(['posts', 'photos', 'videos'] as const).map((mode) => (
              <Button key={mode} variant={mode === tab ? 'default' : 'outline'} aria-pressed={mode === tab} onClick={() => setTab(mode)}>
                {mode[0].toUpperCase() + mode.slice(1)}
              </Button>
            ))}
          </div>
          {tab === 'posts' ? <>
            <RequestError error={feed.error} />
            {feed.isSuccess && posts.length === 0 && <EmptyState>No posts yet.</EmptyState>}
            {posts.map((post) => <PostCard key={post.id} post={post} currentUser={user}
              onUpdatePost={async (postID, content) => { await postsApi.updatePost(postID, { content }); await client.invalidateQueries({ queryKey: ['posts'] }) }}
              onDeletePost={async (postID) => { await postsApi.deletePost(postID); await client.invalidateQueries({ queryKey: ['posts'] }) }} />)}
            {feed.hasNextPage && <Button variant="outline" onClick={() => void feed.fetchNextPage()}>Load more posts</Button>}
          </> : <>
            <RequestError error={media.error} />
            {media.isSuccess && assets.length === 0 && <EmptyState>No {tab} yet.</EmptyState>}
            <div className="grid gap-2 sm:grid-cols-2">{assets.map((asset) => <MediaView key={asset.mediaId} item={asset} label={`${member.nickName}'s attachment`} />)}</div>
            {media.hasNextPage && <Button variant="outline" onClick={() => void media.fetchNextPage()}>Load more {tab}</Button>}
          </>}
        </div>
        <aside className="space-y-4">
          <Card><CardContent className="space-y-3 p-4">
            <h2 className="font-semibold">Find people</h2>
            <form className="flex gap-2" onSubmit={(event) => { event.preventDefault(); setSearch(keyword.trim()) }}>
              <Input type="search" aria-label="Search people" value={keyword} onChange={(event) => setKeyword(event.target.value)} />
              <Button type="submit" size="sm">Search</Button>
            </form>
            <RequestError error={people.error} />
            {people.data && <PeopleList people={people.data} />}
          </CardContent></Card>
          <Card><CardContent className="space-y-2 p-4">
            <h2 className="font-semibold">Suggestions</h2>
            <RequestError error={suggested.error} />
            <PeopleList people={suggested.data?.slice(0, 5) ?? []} />
          </CardContent></Card>
          <Card><CardContent className="space-y-2 p-4">
            <h2 className="font-semibold">Upcoming birthdays</h2>
            <RequestError error={birthdays.error} />
            <PeopleList people={birthdays.data ?? []} />
          </CardContent></Card>
        </aside>
      </div>
    </Section>
  )
}
