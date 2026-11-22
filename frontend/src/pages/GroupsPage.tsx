import { useState } from 'react'
import { useInfiniteQuery } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { UsersRound } from 'lucide-react'
import { Button } from '@/components/ui/button'
import { Card, CardContent } from '@/components/ui/card'
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Input } from '@/components/ui/input'
import { GroupForm } from '@/components/social/GroupForm'
import { EmptyState, RequestError, Section } from '@/components/social/Section'
import { apiFetch } from '@/lib/api'
import type { CursorPage, Group } from '@/types/api'

export default function GroupsPage() {
  const navigate = useNavigate()
  const [joined, setJoined] = useState(true)
  const [creating, setCreating] = useState(false)
  const [search, setSearch] = useState('')
  const groups = useInfiniteQuery({
    queryKey: ['groups', joined, search],
    initialPageParam: '',
    queryFn: ({ pageParam }) => {
      const params = new URLSearchParams({ onlyUserGroups: String(joined), q: search, cursor: pageParam })
      return apiFetch<CursorPage<Group>>(`/api/groups?${params}`)
    },
    getNextPageParam: (page) => page.nextCursor ?? undefined,
  })
  const items = groups.data?.pages.flatMap((page) => page.data) ?? []
  return (
    <Section title="Groups" action={<Button onClick={() => setCreating(true)}>New group</Button>}>
      <div className="flex flex-wrap gap-2" aria-label="Group filters">
        <Button variant={joined ? 'default' : 'outline'} aria-pressed={joined} onClick={() => setJoined(true)}>My Groups</Button>
        <Button variant={joined ? 'outline' : 'default'} aria-pressed={!joined} onClick={() => setJoined(false)}>Explore</Button>
        <Input type="search" aria-label="Search groups" className="sm:max-w-xs" value={search} onChange={(event) => setSearch(event.target.value)} />
      </div>
      <RequestError error={groups.error} />
      {groups.isSuccess && items.length === 0 && <EmptyState>{joined ? 'You have not joined a group yet.' : 'No groups match this search.'}</EmptyState>}
      <div className="grid gap-4 sm:grid-cols-2">
        {items.map((group) => (
          <Card key={group.id}>
            <CardContent className="space-y-2 p-4">
              <div className="flex items-center gap-2">
                <UsersRound className="h-4 w-4 text-primary" aria-hidden="true" />
                <Link to={`/groups/${group.id}`} className="font-semibold hover:underline">{group.name}</Link>
              </div>
              {group.description && <p className="line-clamp-2 text-sm text-muted-foreground">{group.description}</p>}
              <p className="text-xs text-muted-foreground">{group.memberCount} members{group.viewerStatus === 'PENDING' ? ' · Request pending' : ''}</p>
            </CardContent>
          </Card>
        ))}
      </div>
      {groups.hasNextPage && <Button variant="outline" onClick={() => void groups.fetchNextPage()}>Load more groups</Button>}
      <Dialog open={creating} onOpenChange={setCreating}>
        <DialogContent>
          <DialogHeader><DialogTitle>New group</DialogTitle><DialogDescription>Choose a name and add the group details.</DialogDescription></DialogHeader>
          <GroupForm onSaved={(group) => { setCreating(false); navigate(`/groups/${group.id}`) }} />
        </DialogContent>
      </Dialog>
    </Section>
  )
}
