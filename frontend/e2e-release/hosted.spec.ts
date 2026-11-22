import { randomUUID } from 'node:crypto'
import { expect, test } from '@playwright/test'

type Session = { accessToken: string; user: { id: string } }

test('Render Free same-origin demo smoke', async ({ browser, page, context, baseURL }) => {
  const ownerEmail = process.env.RESEND_OWNER_EMAIL
  const ownerPassword = process.env.RESEND_OWNER_PASSWORD
  if (!baseURL || !/^https:\/\/[a-z0-9-]+\.onrender\.com$/.test(baseURL) || !ownerEmail || !ownerPassword) {
    throw new Error('The gated Render origin and Resend owner fixture are required')
  }
  const request = context.request
  await expect.poll(async () => {
    try {
      const response = await request.get('/readyz', { timeout: 6000, maxRedirects: 0 })
      return response.status()
    } catch {
      return 0
    }
  }, { timeout: 105_000, intervals: [1500, 3000, 5000] }).toBe(200)
  expect((await request.get('/healthz')).status()).toBe(200)

  const label = 'release-' + randomUUID().slice(0, 12)
  const email = label + '@example.test'
  const password = randomUUID()
  const registration = await request.post('/api/auth/register', { data: { username: label, email, password } })
  expect(registration.status()).toBe(201)
  const fixture = await registration.json() as Session
  let headers = { Authorization: 'Bearer ' + fixture.accessToken }

  try {
    await page.goto('/')
    await expect(page.getByLabel('Share an update')).toBeVisible()
    await page.reload()
    await expect(page.getByLabel('Share an update')).toBeVisible()
    const refreshCookie = (await context.cookies(new URL('/api/auth/refresh', baseURL).href))
      .find((cookie) => cookie.path === '/api/auth')
    expect(refreshCookie?.httpOnly).toBe(true)
    expect(refreshCookie?.secure).toBe(true)
    expect(refreshCookie?.sameSite).toBe('Lax')
    const rotated = await request.post('/api/auth/refresh')
    expect(rotated.status()).toBe(200)
    headers = { Authorization: 'Bearer ' + (await rotated.json() as Session).accessToken }

    const content = 'I hate this awful release smoke test ' + label
    const photo = Buffer.from('R0lGODlhAQABAIAAAAAAAP///yH5BAEAAAAALAAAAAABAAEAAAIBRAA7', 'base64')
    const presign = await request.post('/api/media/presign', { headers, data: {
      mimeType: 'image/gif', sizeBytes: photo.length, visibility: 'PUBLIC',
    } })
    expect(presign.status()).toBe(201)
    const signed = await presign.json() as { uploadUrl: string; mediaId: string; headers: Record<string, string[]> }
    const uploaded = await request.put(signed.uploadUrl, {
      data: photo, maxRedirects: 0,
      headers: Object.fromEntries(Object.entries(signed.headers).map(([key, values]) => [key, values.join(', ')])),
    })
    expect(uploaded.ok()).toBe(true)
    const finalized = await request.post('/api/media/finalize', { headers, data: { mediaId: signed.mediaId } })
    expect(finalized.status()).toBe(200)
    const publicMedia = await request.get('/api/media/' + signed.mediaId + '/url', { headers })
    const { url } = await publicMedia.json() as { url: string }
    expect((await request.get(url)).status()).toBe(200)
    const posted = await request.post('/api/posts', { headers, data: { content, mediaIds: [signed.mediaId] } })
    expect(posted.status()).toBe(201)
    const post = await posted.json() as { id: string; sentiment: string; contentVersion: number }
    expect(post.sentiment).toBe('BAD')
    expect(post.contentVersion).toBe(1)
    const feed = await request.post('/graphql', { headers, data: { query: '{ feed(first: 20) { data { id sentiment } } }' } })
    expect(feed.status()).toBe(200)
    expect((await feed.json()).data.feed.data).toContainEqual({ id: post.id, sentiment: 'BAD' })

    const ticket = await request.post('/api/auth/ws-ticket', { headers })
    expect(ticket.status()).toBe(201)
    const { ticket: value } = await ticket.json() as { ticket: string }
    const connected = await page.evaluate((wsTicket) => new Promise<boolean>((resolve) => {
      const local = new URL('/ws', window.location.origin)
      local.protocol = 'wss:'
      local.searchParams.set('ticket', wsTicket)
      const ws = new WebSocket(local)
      const timer = setTimeout(() => { ws.close(); resolve(false) }, 6000)
      ws.addEventListener('message', ({ data }) => {
        if (JSON.parse(String(data)).type === 'PRESENCE_SNAPSHOT') {
          clearTimeout(timer)
          ws.close()
          resolve(true)
        }
      })
    }), value)
    expect(connected).toBe(true)

    const secondContext = await browser.newContext({ baseURL })
    try {
      const secondRegistered = await secondContext.request.post('/api/auth/register', {
        data: { username: label + '-peer', email: label + '-peer@example.test', password },
      })
      expect(secondRegistered.status()).toBe(201)
      const second = await secondRegistered.json() as Session
      const peerHeaders = { Authorization: 'Bearer ' + second.accessToken }
      try {
        const room = await request.post('/api/chat/rooms', {
          headers, data: { type: 'DIRECT', participantIds: [second.user.id] },
        })
        expect(room.status()).toBe(201)
        const { id: roomId } = await room.json() as { id: string }
        const path = '/api/chat/rooms/' + roomId + '/messages'
        const sent = await request.post(path, { headers, data: { content: label } })
        expect(sent.status()).toBe(201)
        const sync = await secondContext.request.get('/api/chat/rooms/sync', { headers: peerHeaders })
        expect(sync.status()).toBe(200)
        const catchup = await secondContext.request.get(path + '?afterSeq=0', { headers: peerHeaders })
        expect(catchup.status()).toBe(200)
        expect((await catchup.json()).data).toEqual(expect.arrayContaining([
          expect.objectContaining({ content: label, roomSeq: 1 }),
        ]))
      } finally {
        await secondContext.request.delete('/api/users/' + second.user.id, { headers: peerHeaders })
      }
    } finally {
      await secondContext.close()
    }

    const rejected = await request.post('/api/auth/forgot-password', { data: { email } })
    expect(rejected.status()).toBe(503)
    expect((await rejected.json()).error).toBe('DEMO_RECIPIENT_RESTRICTED')
  } finally {
    await request.delete('/api/users/' + fixture.user.id, { headers })
  }

  const owner = await request.post('/api/auth/login', { data: { email: ownerEmail, password: ownerPassword } })
  expect(owner.status()).toBe(200)
  const reset = await request.post('/api/auth/forgot-password', { data: { email: ownerEmail } })
  expect(reset.status()).toBe(200)
})
