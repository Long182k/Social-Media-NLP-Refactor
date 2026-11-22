import { execFileSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'
import { expect, test, type Browser, type BrowserContext, type Page } from '@playwright/test'

const photo = {
  name: 'tiny.gif',
  mimeType: 'image/gif',
  buffer: Buffer.from('R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7', 'base64'),
}

type Member = {
  id: string
  name: string
  username: string
  accessToken: string
  context: BrowserContext
  page: Page
  errors: string[]
}

async function member(browser: Browser, baseURL: string | undefined, label: string, admin = false): Promise<Member> {
  const name = `${label} ${randomUUID().slice(0, 8)}`
  const username = name.toLowerCase().replaceAll(' ', '_')
  const context = await browser.newContext({ baseURL, permissions: ['clipboard-read', 'clipboard-write'] })
  const response = await context.request.post('/api/auth/register', { data: {
    username, nickName: name, email: `${username}@example.test`, password: 'Password123!',
  } })
  expect(response.status()).toBe(201)
  const { user, accessToken } = await response.json() as { user: { id: string }; accessToken: string }
  if (admin) {
    expect(user.id).toMatch(/^[\da-f-]{36}$/)
    execFileSync(process.env.CONTAINER_ENGINE || 'kiri', [
      'compose', '-f', '../deploy/compose.yaml', 'exec', '-T', 'postgres',
      'psql', '-U', 'postgres', '-d', 'social_media', '-v', 'ON_ERROR_STOP=1',
      '-c', `UPDATE users SET role = 'ADMIN' WHERE id = '${user.id}'`,
    ])
  }
  const page = await context.newPage()
  const errors: string[] = []
  page.on('pageerror', (error) => errors.push(error.message))
  page.on('console', (message) => {
    if (message.type() === 'error') errors.push(message.text())
  })
  page.on('response', (res) => {
    if (res.status() >= 400) errors.push(`HTTP ${res.status()} ${new URL(res.url()).pathname}`)
  })
  await page.goto('/')
  await expect(page.getByLabel('Share an update')).toBeVisible()
  return { id: user.id, name, username, accessToken, context, page, errors }
}

async function closeMembers(...members: Member[]) {
  for (const actor of members) {
    await actor.context.close()
    expect(actor.errors).toEqual([])
  }
}

test.describe('Milestone 2 social parity', () => {
  test('public attachments, comments, likes, bookmarks, notifications and profile tabs', async ({ browser, baseURL }) => {
    const owner = await member(browser, baseURL, 'Author')
    const visitor = await member(browser, baseURL, 'Reader')
    const content = `Good update ${randomUUID()}`
    await owner.page.getByLabel('Share an update').fill(content)
    await owner.page.getByLabel('Attachment', { exact: true }).setInputFiles(photo)
    await owner.page.getByRole('button', { name: 'Post', exact: true }).click()
    const card = owner.page.getByTestId('post-card').filter({ hasText: content })
    await expect(card.getByRole('img', { name: 'Post attachment' })).toBeVisible()
    await expect.poll(() => card.getByRole('img', { name: 'Post attachment' }).evaluate((img: HTMLImageElement) => img.naturalWidth)).toBeGreaterThan(0)
    const postId = await card.getAttribute('data-post-id')

    await visitor.page.goto(`/posts/${postId}`)
    await visitor.page.getByRole('button', { name: /^Like/ }).click()
    await expect(visitor.page.getByRole('button', { name: /^Like/ })).toHaveAttribute('aria-pressed', 'true')
    await visitor.page.getByRole('button', { name: 'Bookmark', exact: true }).click()
    await expect(visitor.page.getByRole('button', { name: 'Saved' })).toBeVisible()
    await visitor.page.getByRole('link', { name: 'Bookmarks', exact: true }).click()
    await expect(visitor.page.getByRole('link', { name: content })).toBeVisible()
    await visitor.page.getByRole('button', { name: 'Remove bookmark' }).click()
    await expect(visitor.page.getByText('Saved posts appear here.')).toBeVisible()

    await visitor.page.goto(`/posts/${postId}`)
    await visitor.page.getByLabel('Write a comment').fill('I love this update')
    await visitor.page.getByRole('button', { name: 'Post comment' }).click()
    await expect(visitor.page.getByText('I love this update', { exact: true })).toBeVisible()
    await visitor.page.getByRole('button', { name: 'Edit comment', exact: true }).click()
    await visitor.page.getByLabel('Edit comment').fill('Thank you for the update')
    await visitor.page.getByRole('button', { name: 'Save comment' }).click()
    await expect(visitor.page.getByText('Thank you for the update', { exact: true })).toBeVisible()

    await visitor.page.goto(`/profile/${owner.id}`)
    await visitor.page.getByRole('button', { name: 'Follow', exact: true }).click()
    await expect(visitor.page.getByRole('button', { name: 'Unfollow' })).toBeVisible()
    await visitor.page.getByRole('button', { name: 'Photos', exact: true }).click()
    await expect(visitor.page.getByRole('img', { name: `${owner.name}'s attachment` })).toBeVisible()
    await visitor.page.getByRole('button', { name: 'Videos', exact: true }).click()
    await expect(visitor.page.getByText('No videos yet.')).toBeVisible()

    await owner.page.getByRole('link', { name: /^Notifications/ }).click()
    await expect(owner.page.getByRole('link', { name: 'Liked your post' })).toBeVisible()
    await expect(owner.page.getByRole('link', { name: 'Commented on your post' })).toBeVisible()
    await owner.page.getByRole('button', { name: 'Mark all as read' }).click()
    await expect(owner.page.getByRole('button', { name: 'Mark as read', exact: true })).toHaveCount(0)
    const likeNotice = owner.page.getByRole('link', { name: 'Liked your post' })
    await likeNotice.locator('xpath=../..').getByRole('button', { name: 'Delete notification' }).click()
    await expect(likeNotice).toHaveCount(0)

    await owner.page.getByRole('link', { name: 'Settings', exact: true }).click()
    await owner.page.getByLabel('Bio', { exact: true }).fill('Profile metadata saved in the browser')
    await owner.page.getByRole('button', { name: 'Save profile' }).click()
    await expect(owner.page.getByText('Profile saved.')).toBeVisible()
    await owner.page.getByLabel('Current password').fill('Password123!')
    await owner.page.getByLabel('New password').fill('NewPassword123!')
    await owner.page.getByRole('button', { name: 'Change password' }).click()
    await expect(owner.page.getByText('Password changed.')).toBeVisible()
    await closeMembers(owner, visitor)
  })

  test('group membership, group feed, events, and admin management', async ({ browser, baseURL }) => {
    const owner = await member(browser, baseURL, 'Organizer')
    const visitor = await member(browser, baseURL, 'Attendee')
    const admin = await member(browser, baseURL, 'Moderator', true)
    const group = `Workshop group ${randomUUID().slice(0, 8)}`
    await owner.page.getByRole('link', { name: 'Groups', exact: true }).click()
    await owner.page.getByRole('button', { name: 'New group' }).click()
    await owner.page.getByLabel('Group name').fill(group)
    await owner.page.getByRole('button', { name: 'Create group' }).click()
    await expect(owner.page.getByRole('heading', { name: group })).toBeVisible()
    const groupURL = owner.page.url()
    await owner.page.getByRole('button', { name: 'Copy group link' }).click()
    await expect(owner.page.getByText('Group link copied.')).toBeVisible()
    await visitor.page.getByRole('link', { name: 'Groups', exact: true }).click()
    await visitor.page.getByRole('button', { name: 'Explore', exact: true }).click()
    await visitor.page.getByRole('link', { name: group }).click()
    await visitor.page.getByRole('button', { name: 'Join group' }).click()
    await expect(visitor.page.getByRole('button', { name: 'Cancel join request' })).toBeVisible()
    await owner.page.reload()
    await owner.page.getByRole('button', { name: 'Approve', exact: true }).click()
    await visitor.page.reload()
    await expect(visitor.page.getByRole('button', { name: 'Leave group' })).toBeVisible()
    const groupPost = `Group-only message ${randomUUID()}`
    await visitor.page.getByLabel('Group post', { exact: true }).fill(groupPost)
    await visitor.page.getByRole('button', { name: 'Post to group' }).click()
    await expect(visitor.page.getByText(groupPost, { exact: true })).toBeVisible()
    await visitor.page.getByRole('link', { name: 'Home', exact: true }).click()
    await expect(visitor.page.getByLabel('Feed')).toBeVisible()
    await expect(visitor.page.getByText(groupPost, { exact: true })).toHaveCount(0)
    await visitor.page.goto(groupURL)
    await visitor.page.getByRole('button', { name: 'Leave group' }).click()
    await expect(visitor.page.getByRole('button', { name: 'Join group' })).toBeVisible()

    const event = `Technology meetup ${randomUUID().slice(0, 8)}`
    await owner.page.getByRole('link', { name: 'Events', exact: true }).click()
    await owner.page.getByRole('button', { name: 'New event' }).click()
    await owner.page.getByLabel('Event name').fill(event)
    await owner.page.getByLabel('Description').fill('Local technology meetup')
    await owner.page.getByLabel('Date and time').fill('2027-01-01T19:00')
    await owner.page.getByLabel('Category', { exact: true }).selectOption('TECHNOLOGY')
    await owner.page.getByRole('button', { name: 'Create event' }).click()
    await expect(owner.page.getByRole('heading', { name: event })).toBeVisible()
    await visitor.page.getByRole('link', { name: 'Events', exact: true }).click()
    await visitor.page.getByLabel('Event category').selectOption('TECHNOLOGY')
    await visitor.page.getByRole('link', { name: event }).click()
    await visitor.page.getByRole('button', { name: 'RSVP', exact: true }).click()
    await expect(visitor.page.getByText('RSVP request pending')).toBeVisible()
    await owner.page.reload()
    await owner.page.getByRole('button', { name: 'Approve RSVP' }).click()
    await visitor.page.reload()
    await expect(visitor.page.getByRole('button', { name: 'Cancel RSVP', exact: true })).toBeVisible()
    await closeMembers(visitor)

    await admin.page.getByRole('link', { name: 'Admin', exact: true }).click()
    await expect(admin.page.getByText('Active in the past 24 hours')).toBeVisible()
    await expect(admin.page.getByText('New users in the past 7 days')).toBeVisible()
    await expect(admin.page.getByText('Most positive user')).toBeVisible()
    await admin.page.getByRole('button', { name: 'Users', exact: true }).click()
    await admin.page.getByLabel('Search users').fill(visitor.username)
    await expect(admin.page.getByText(visitor.name, { exact: false }).first()).toBeVisible()
    await admin.page.getByRole('button', { name: 'Deactivate user' }).click()
    await admin.page.getByRole('dialog').getByRole('button', { name: 'Deactivate user' }).click()
    await expect(admin.page.getByRole('button', { name: 'Activate user' })).toBeVisible()
    await admin.page.getByRole('button', { name: 'Groups', exact: true }).click()
    const groupEntry = admin.page.getByText(group, { exact: true }).locator('xpath=../..')
    await groupEntry.getByRole('button', { name: 'Delete group' }).click()
    await admin.page.getByRole('dialog').getByRole('button', { name: 'Delete group' }).click()
    await expect(admin.page.getByText(group, { exact: true })).toHaveCount(0)
    await admin.page.getByRole('button', { name: 'Events', exact: true }).click()
    const eventEntry = admin.page.getByText(event, { exact: true }).locator('xpath=../..')
    await eventEntry.getByRole('button', { name: 'Delete event' }).click()
    await admin.page.getByRole('dialog').getByRole('button', { name: 'Delete event' }).click()
    await expect(admin.page.getByText(event, { exact: true })).toHaveCount(0)
    await closeMembers(owner, admin)
  })

  test('private chat media, live tabs, and inactive room catch-up', async ({ browser, baseURL }) => {
    const sender = await member(browser, baseURL, 'Sender')
    const receiver = await member(browser, baseURL, 'Receiver')
    const outsider = await member(browser, baseURL, 'Other')
    await sender.page.goto(`/profile/${receiver.id}`)
    await sender.page.getByRole('button', { name: 'Message', exact: true }).click()
    await expect(sender.page.getByRole('heading', { name: 'Messages' })).toBeVisible()
    await expect(sender.page.getByText('Live updates connected')).toBeVisible()
    const roomURL = sender.page.url()
    await receiver.page.goto(roomURL)
    await expect(receiver.page.getByText('Live updates connected')).toBeVisible()
    const tab = await receiver.context.newPage()
    await tab.goto(roomURL)
    await expect(tab.getByText('Live updates connected')).toBeVisible()
    const text = `Hello https://example.test/${randomUUID()}`
    await sender.page.getByLabel('Message', { exact: true }).fill(text)
    await sender.page.getByLabel('Private attachment').setInputFiles(photo)
    const messageResponse = sender.page.waitForResponse((res) => res.url().endsWith('/messages') && res.request().method() === 'POST')
    await sender.page.getByRole('button', { name: 'Send message' }).click()
    const response = await messageResponse
    expect(response.status()).toBe(201)
    const sent = await response.json() as { attachments: { mediaId: string }[] }
    const mediaId = sent.attachments[0].mediaId
    for (const page of [receiver.page, tab]) {
      await expect(page.getByRole('list', { name: 'Messages' }).getByText(text)).toBeVisible()
      await expect(page.getByRole('img', { name: 'Private chat attachment' })).toBeVisible()
    }
    const denied = await outsider.context.request.get(`/api/media/${mediaId}/url`, { headers: { Authorization: `Bearer ${outsider.accessToken}` } })
    expect(denied.status()).toBe(403)
    await receiver.page.getByRole('button', { name: 'Shared links' }).click()
    await expect(receiver.page.getByRole('link', { name: text.replace('Hello ', ''), exact: true })).toBeVisible()
    await tab.close()
    await receiver.page.close()

    const missed = `Missed room message ${randomUUID()}`
    await sender.page.getByLabel('Message', { exact: true }).fill(missed)
    await sender.page.getByRole('button', { name: 'Send message' }).click()
    await expect(sender.page.getByRole('list', { name: 'Messages' }).getByText(missed)).toBeVisible()
    const reconnect = await receiver.context.newPage()
    await reconnect.goto('/messages')
    const inactive = reconnect.getByRole('link', { name: sender.name })
    await expect(inactive).toContainText(missed)
    await inactive.click()
    await expect(reconnect.getByRole('list', { name: 'Messages' }).getByText(missed)).toBeVisible()
    await closeMembers(sender, receiver, outsider)
  })
})
