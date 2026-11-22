import { useMutation, useQueryClient } from '@tanstack/react-query'
import { Link, useNavigate } from 'react-router-dom'
import { ConfirmButton } from '@/components/social/ConfirmButton'
import { RequestError, Section } from '@/components/social/Section'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Input } from '@/components/ui/input'
import { Textarea } from '@/components/ui/textarea'
import { broadcastSessionCleared } from '@/lib/api'
import { mediaAccept, send, uploadMedia } from '@/lib/socialApi'
import { useUIStore } from '@/stores/uiStore'
import type { Profile } from '@/types/api'

export default function SettingsPage() {
  const user = useUIStore((state) => state.user)
  const theme = useUIStore((state) => state.theme)
  const toggleTheme = useUIStore((state) => state.toggleTheme)
  const navigate = useNavigate()
  const client = useQueryClient()

  async function updateSession(profile: Profile) {
    const session = useUIStore.getState()
    if (session.accessToken && session.user?.id === profile.id) {
      session.setSession(session.accessToken, {
        ...session.user,
        nickName: profile.nickName, bio: profile.bio ?? null, dateOfBirth: profile.dateOfBirth ?? null,
        avatarUrl: profile.avatarUrl, coverPageUrl: profile.coverPageUrl,
      })
    }
    await client.invalidateQueries({ queryKey: ['profile', profile.id] })
  }

  const saveProfile = useMutation({
    mutationFn: (data: FormData) => {
      const birth = String(data.get('birth') ?? '')
      return send<Profile>('/api/users/edit-profile', 'PATCH', {
        nickName: data.get('nickname'), bio: data.get('bio'), dateOfBirth: birth ? new Date(`${birth}T00:00:00Z`).toISOString() : undefined,
      })
    },
    onSuccess: updateSession,
  })
  const saveImage = useMutation({
    mutationFn: async ({ file, target }: { file: File; target: 'avatar' | 'cover-page' }) => {
      const image = await uploadMedia(file, 'PUBLIC')
      return send<Profile>(`/api/users/change/${target}`, 'PATCH', { mediaId: image.mediaId })
    },
    onSuccess: updateSession,
  })
  const changePassword = useMutation({
    mutationFn: (data: FormData) => send('/api/auth/change-password', 'POST', { oldPassword: data.get('old'), newPassword: data.get('next') }),
  })
  const resetPassword = useMutation({
    mutationFn: () => send('/api/auth/forgot-password', 'POST', { email: user?.email }),
  })
  if (!user) return null
  return (
    <Section title="Settings" action={<Button variant="outline" asChild><Link to={`/profile/${user.id}`}>View profile</Link></Button>}>
      <div className="grid items-start gap-4 lg:grid-cols-2">
        <Card>
          <CardHeader><CardTitle>Profile</CardTitle></CardHeader>
          <CardContent className="space-y-4">
            <form className="space-y-3" onSubmit={(event) => { event.preventDefault(); saveProfile.mutate(new FormData(event.currentTarget)) }}>
              <label className="grid gap-1 text-sm font-medium">Display name<Input name="nickname" required maxLength={100} defaultValue={user.nickName} /></label>
              <label className="grid gap-1 text-sm font-medium">Bio<Textarea name="bio" rows={3} maxLength={1000} defaultValue={user.bio ?? ''} /></label>
              <label className="grid gap-1 text-sm font-medium">Birthday<Input name="birth" type="date" defaultValue={user.dateOfBirth?.slice(0, 10)} /></label>
              <RequestError error={saveProfile.error} />
              {saveProfile.isSuccess && <p role="status" className="text-sm">Profile saved.</p>}
              <Button type="submit" disabled={saveProfile.isPending}>Save profile</Button>
            </form>
            <form className="space-y-3 border-t border-border pt-4" onSubmit={(event) => {
              event.preventDefault()
              const fields = new FormData(event.currentTarget)
              const file = fields.get('file')
              const target = fields.get('target')
              if (file instanceof File && (target === 'avatar' || target === 'cover-page')) saveImage.mutate({ file, target })
            }}>
              <label className="grid gap-1 text-sm font-medium">Image type
                <select name="target" className="h-9 rounded-md border border-input bg-background px-2">
                  <option value="avatar">Avatar</option><option value="cover-page">Cover photo</option>
                </select>
              </label>
              <label className="grid gap-1 text-sm font-medium">Profile image<Input name="file" type="file" accept={mediaAccept.replace(',video/mp4', '')} required /></label>
              <RequestError error={saveImage.error} />
              {saveImage.isSuccess && <p role="status" className="text-sm">Image saved.</p>}
              <Button type="submit" disabled={saveImage.isPending}>Save image</Button>
            </form>
          </CardContent>
        </Card>
        <div className="space-y-4">
          <Card>
            <CardHeader><CardTitle>Password</CardTitle></CardHeader>
            <CardContent className="space-y-4">
              <form className="space-y-3" onSubmit={(event) => {
                event.preventDefault()
                const form = event.currentTarget
                changePassword.mutate(new FormData(form), { onSuccess: () => form.reset() })
              }}>
                <label className="grid gap-1 text-sm font-medium">Current password<Input type="password" name="old" autoComplete="current-password" required /></label>
                <label className="grid gap-1 text-sm font-medium">New password<Input type="password" name="next" autoComplete="new-password" minLength={6} required /></label>
                <Button type="submit" disabled={changePassword.isPending}>Change password</Button>
                <RequestError error={changePassword.error} />
                {changePassword.isSuccess && <p role="status" className="text-sm">Password changed.</p>}
              </form>
              <Button variant="outline" onClick={() => resetPassword.mutate()} disabled={resetPassword.isPending}>Email a password reset link</Button>
              <RequestError error={resetPassword.error} />
              {resetPassword.isSuccess && <p role="status" className="text-sm">Reset link requested.</p>}
            </CardContent>
          </Card>
          <Card><CardContent className="flex flex-wrap items-center justify-between gap-3 p-4">
            <span className="text-sm font-medium">Appearance</span>
            <Button variant="outline" onClick={toggleTheme}>{theme === 'dark' ? 'Use light mode' : 'Use dark mode'}</Button>
          </CardContent></Card>
          <ConfirmButton label="Delete account" description="Your profile and its content will be removed." onConfirm={async () => {
            await send(`/api/users/${user.id}`, 'DELETE')
            useUIStore.getState().clearSession()
            broadcastSessionCleared()
            client.clear()
            navigate('/login', { replace: true })
          }} />
        </div>
      </div>
    </Section>
  )
}
