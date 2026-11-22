import { useState } from 'react'
import { useInfiniteQuery, useQuery, useQueryClient } from '@tanstack/react-query'
import { Navigate } from 'react-router-dom'
import { ConfirmButton } from '@/components/social/ConfirmButton'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { apiFetch } from '@/lib/api'
import { queryGraph } from '@/lib/graphApi'
import { send } from '@/lib/socialApi'
import { useUIStore } from '@/stores/uiStore'
import type { CursorPage, Sentiment } from '@/types/api'

type Ratios = Record<Sentiment, { count: number; ratio: number }>
type Ranking = { nickName: string; postCount: number; commentCount: number; totalCount: number }
type Dashboard = {
  totalUsers: number; activeUsers: number; totalPosts: number; totalGroups: number; totalEvents: number
  sentimentRatios: Ratios; userGrowthData: { day: string; count: number }[]
  mostPositiveUser: Ranking | null; mostNegativeUser: Ranking | null
}
type AdminUser = {
  id: string; userName: string; nickName: string; email: string; role: string; isActive: boolean
  posts: number; comments: number; likes: number; bookmarks: number; followers: number; following: number
  postSentiment: Ratios; commentSentiment: Ratios
}
type ManagedResource = { id: string; name: string; member_count?: number; attendee_count?: number }
type Tab = 'Overview' | 'Users' | 'Groups' | 'Events'

export default function AdminPage() {
  const role = useUIStore((state) => state.user?.role)
  const [tab, setTab] = useState<Tab>('Overview')
  if (role !== 'ADMIN') return <Navigate to="/" replace />
  return (
    <Section title="Administration">
      <div className="flex flex-wrap gap-2">
        {(['Overview', 'Users', 'Groups', 'Events'] as const).map((item) => (
          <Button key={item} variant={item === tab ? 'default' : 'outline'} aria-pressed={item === tab} onClick={() => setTab(item)}>{item}</Button>
        ))}
      </div>
      {tab === 'Overview' && <Overview />}
      {tab === 'Users' && <UserManagement />}
      {(tab === 'Groups' || tab === 'Events') && <ResourceManagement key={tab} kind={tab === 'Groups' ? 'groups' : 'events'} />}
    </Section>
  )
}

function Overview() {
  const stats = useQuery({
    queryKey: ['admin', 'overview'],
    queryFn: async () => {
      type GraphDashboard = Omit<Dashboard, 'sentimentRatios'> & { sentimentRatios: { label: Sentiment; count: number; ratio: number }[] }
      const { adminDashboardStats } = await queryGraph<{ adminDashboardStats: GraphDashboard }>(`
        query AdminDashboard {
          adminDashboardStats {
            totalUsers activeUsers totalPosts totalGroups totalEvents
            sentimentRatios { label count ratio }
            userGrowthData { day count }
            mostPositiveUser { nickName postCount commentCount totalCount }
            mostNegativeUser { nickName postCount commentCount totalCount }
          }
        }
      `)
      const ratios = Object.fromEntries(adminDashboardStats.sentimentRatios.map(({ label, count, ratio }) => [label, { count, ratio }])) as Ratios
      return { ...adminDashboardStats, sentimentRatios: ratios }
    },
  })
  if (!stats.data) return <RequestError error={stats.error} />
  const data = stats.data
  const metrics = [
    ['Users', data.totalUsers], ['Active in the past 24 hours', data.activeUsers],
    ['Posts', data.totalPosts], ['Groups', data.totalGroups], ['Events', data.totalEvents],
  ] as const
  const maxGrowth = Math.max(1, ...data.userGrowthData.map((item) => item.count))
  return (
    <div className="space-y-4">
      <div className="grid gap-3 sm:grid-cols-3 lg:grid-cols-5">
        {metrics.map(([label, count]) => <Card key={label}><CardContent className="space-y-1 p-4">
          <p className="text-sm text-muted-foreground">{label}</p><p className="text-2xl font-semibold tabular-nums">{count}</p>
        </CardContent></Card>)}
      </div>
      <div className="grid items-start gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>New users in the past 7 days</CardTitle></CardHeader>
          <CardContent className="space-y-2">
            {data.userGrowthData.map(({ day, count }) => (
              <div key={day} className="grid grid-cols-[7rem_1fr_2.5rem] items-center gap-2 text-sm">
                <time dateTime={day}>{day}</time>
                <div className="h-2 rounded-full bg-muted"><div aria-hidden="true" className="h-full rounded-full bg-primary" style={{ width: `${100 * count / maxGrowth}%` }} /></div>
                <span className="text-right tabular-nums">{count}</span>
              </div>
            ))}
          </CardContent>
        </Card>
        <Card>
          <CardHeader><CardTitle>Sentiment</CardTitle></CardHeader>
          <CardContent className="space-y-4">
            <RatioSummary ratios={data.sentimentRatios} />
            <RankingPanel title="Most positive user" ranking={data.mostPositiveUser} />
            <RankingPanel title="Most negative user" ranking={data.mostNegativeUser} />
          </CardContent>
        </Card>
      </div>
    </div>
  )
}

function RankingPanel({ title, ranking }: { title: string; ranking: Ranking | null }) {
  return <div className="space-y-1 border-t border-border pt-3">
    <h3 className="text-sm font-semibold">{title}</h3>
    {ranking ? <p className="text-sm">{ranking.nickName} · {ranking.postCount} posts · {ranking.commentCount} comments · {ranking.totalCount} total</p> : <p className="text-sm text-muted-foreground">No qualifying content.</p>}
  </div>
}

function RatioSummary({ ratios }: { ratios: Ratios }) {
  return <div className="flex flex-wrap gap-1.5">
    {(['GOOD', 'MODERATE', 'BAD'] as const).map((label) => <Badge key={label} variant="outline" title={`${ratios[label].count} items`}>
      {label} {Math.round(ratios[label].ratio * 100)}%
    </Badge>)}
  </div>
}

function UserManagement() {
  const client = useQueryClient()
  const [search, setSearch] = useState('')
  const users = useInfiniteQuery({
    queryKey: ['admin', 'users', search],
    initialPageParam: '',
    queryFn: ({ pageParam }) => apiFetch<CursorPage<AdminUser>>(`/api/admin/users?${new URLSearchParams({ q: search, cursor: pageParam })}`),
    getNextPageParam: (page) => page.nextCursor ?? undefined,
  })
  const items = users.data?.pages.flatMap((page) => page.data) ?? []
  return <div className="space-y-3">
    <h2 className="text-lg font-semibold">Manage users</h2>
    <Input type="search" className="max-w-xs" aria-label="Search users" value={search} onChange={(event) => setSearch(event.target.value)} />
    <RequestError error={users.error} />
    {users.isSuccess && items.length === 0 && <EmptyState>No users match this search.</EmptyState>}
    {items.map((user) => <Card key={user.id}><CardContent className="flex flex-wrap items-start gap-4 p-4">
      <div className="min-w-0 flex-1 space-y-2">
        <p className="break-words font-semibold">{user.nickName} <span className="text-sm font-normal text-muted-foreground">@{user.userName}</span></p>
        <p className="break-words text-sm text-muted-foreground">{user.email} · {user.role} · {user.isActive ? 'Active' : 'Inactive'}</p>
        <p className="text-sm">{user.followers} followers · {user.following} following · {user.likes} likes · {user.bookmarks} bookmarks</p>
        <div><span className="text-xs font-medium">{user.posts} Posts</span><RatioSummary ratios={user.postSentiment} /></div>
        <div><span className="text-xs font-medium">{user.comments} Comments</span><RatioSummary ratios={user.commentSentiment} /></div>
      </div>
      <ConfirmButton label={user.isActive ? 'Deactivate user' : 'Activate user'} description={`Change account access for ${user.nickName}.`} onConfirm={async () => {
        await send(`/api/admin/users/${user.id}/toggle-activity`, 'PATCH')
        await client.invalidateQueries({ queryKey: ['admin'] })
      }} />
    </CardContent></Card>)}
    {users.hasNextPage && <Button variant="outline" onClick={() => void users.fetchNextPage()}>Load more users</Button>}
  </div>
}

function ResourceManagement({ kind }: { kind: 'groups' | 'events' }) {
  const client = useQueryClient()
  const resources = useInfiniteQuery({
    queryKey: ['admin', kind],
    initialPageParam: 0,
    queryFn: ({ pageParam }) => apiFetch<ManagedResource[]>(`/api/admin/${kind}?offset=${pageParam}`),
    getNextPageParam: (page, _pages, offset) => page.length === 50 ? offset + 50 : undefined,
  })
  const items = resources.data?.pages.flat() ?? []
  return <div className="space-y-3">
    <h2 className="text-lg font-semibold">Manage {kind}</h2>
    <RequestError error={resources.error} />
    {resources.isSuccess && items.length === 0 && <EmptyState>No {kind} to manage.</EmptyState>}
    {items.map((item) => <Card key={item.id}><CardContent className="flex flex-wrap items-center gap-3 p-4">
      <div className="mr-auto">
        <p className="font-medium">{item.name}</p>
        <p className="text-xs text-muted-foreground">{kind === 'groups' ? `${item.member_count} members` : `${item.attendee_count} attendees`}</p>
      </div>
      <ConfirmButton label={kind === 'groups' ? 'Delete group' : 'Delete event'} description={`Remove ${item.name} and its related records.`} onConfirm={async () => {
        await send(`/api/admin/${kind}/${item.id}`, 'DELETE')
        await client.invalidateQueries({ queryKey: ['admin'] })
      }} />
    </CardContent></Card>)}
    {resources.hasNextPage && <Button variant="outline" onClick={() => void resources.fetchNextPage()}>Load more {kind}</Button>}
  </div>
}
