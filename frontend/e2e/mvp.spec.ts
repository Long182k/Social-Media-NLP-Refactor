import { expect, request as playwrightRequest, test } from '@playwright/test'

test.describe('Milestone 1 End-to-End Vertical Slice', () => {
  test('register, silent reload restore, dark mode non-remount, VI/EN sentiment CRUD, search, cursor infinite scroll, and signout', async ({
    page,
    baseURL,
  }) => {
    const errors: string[] = []
    let isAuthenticated = false
    let unauthRefreshHttp401 = 0
    let unauthRefreshConsole401 = 0

    page.on('pageerror', (err) => {
      errors.push(`pageerror: ${err.message}`)
    })

    page.on('response', (res) => {
      const status = res.status()
      if (status >= 500) {
        errors.push(`HTTP ${status} ${res.request().method()} ${res.url()}`)
        return
      }
      if (status >= 400) {
        const pathname = new URL(res.url()).pathname
        if (
          !isAuthenticated &&
          status === 401 &&
          res.request().method() === 'POST' &&
          pathname === '/api/auth/refresh' &&
          unauthRefreshHttp401 < 2
        ) {
          unauthRefreshHttp401 += 1
          return
        }
        errors.push(`Unexpected HTTP ${status} ${res.request().method()} ${res.url()}`)
      }
    })

    page.on('console', (msg) => {
      if (msg.type() !== 'error') return
      const locUrl = msg.location().url || ''
      const text = msg.text()
      if (
        !isAuthenticated &&
        locUrl.includes('/api/auth/refresh') &&
        text.includes('401') &&
        unauthRefreshConsole401 < 2
      ) {
        unauthRefreshConsole401 += 1
        return
      }
      errors.push(`console.error: ${text} (${locUrl})`)
    })

    const runId = `${Date.now()}_${Math.floor(Math.random() * 10000)}`
    const username = `e2e_${runId}`
    const email = `${username}@example.com`
    const password = 'Password123!'

    // 1. Register a unique user -> redirect to /
    await page.goto('/register')
    await page.getByLabel('Username').fill(username)
    await page.getByLabel(/Display name/i).fill(`E2E User ${runId}`)
    await page.getByLabel('Email').fill(email)
    await page.getByLabel(/^Password$/i).fill(password)
    await page.getByLabel(/Confirm password/i).fill(password)

    isAuthenticated = true
    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByLabel('Share an update')).toBeVisible()

    // Verify localStorage & sessionStorage contain zero auth/token keys
    const storageKeys = await page.evaluate(() => ({
      localKeys: Object.keys(window.localStorage),
      sessionKeys: Object.keys(window.sessionStorage),
    }))
    expect(storageKeys.sessionKeys).toEqual([])
    for (const key of storageKeys.localKeys) {
      expect(key).not.toMatch(/token|auth|jwt|session|user/i)
    }

    // Provision >= 11 fixture posts via authenticated API calls so cursor infinite scroll
    // works reliably even on a completely clean migrated database without prior cmd/seed.
    const fixtureCtx = await playwrightRequest.newContext({ baseURL })
    try {
      const loginResp = await fixtureCtx.post('/api/auth/login', {
        data: { identifier: username, password },
      })
      expect(loginResp.status()).toBe(200)
      const { accessToken } = (await loginResp.json()) as { accessToken: string }
      for (let i = 1; i <= 11; i += 1) {
        const postResp = await fixtureCtx.post('/api/posts', {
          headers: { Authorization: `Bearer ${accessToken}` },
          data: { content: `Clean fixture post ${i} for scroll pagination #fixture_${runId}` },
        })
        expect(postResp.status()).toBe(201)
      }
    } finally {
      await fixtureCtx.dispose()
    }

    // 2. Type draft -> toggle dark mode -> verify .dark class & draft preserved -> reload
    const composer = page.getByLabel('Share an update')
    const draftText = `Draft kept across theme toggle ${runId}`
    await composer.fill(draftText)

    const initialDark = await page.evaluate(() =>
      document.documentElement.classList.contains('dark'),
    )
    await page.getByRole('button', { name: 'Toggle theme' }).click()
    const afterToggleDark = await page.evaluate(() =>
      document.documentElement.classList.contains('dark'),
    )
    expect(afterToggleDark).toBe(!initialDark)
    await expect(composer).toHaveValue(draftText)

    // Reload: silent POST /api/auth/refresh restores session & keeps user on /
    await page.reload()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByLabel('Share an update')).toBeVisible()

    const storageAfterReload = await page.evaluate(() => ({
      localKeys: Object.keys(window.localStorage),
      sessionKeys: Object.keys(window.sessionStorage),
    }))
    expect(storageAfterReload.sessionKeys).toEqual([])
    for (const key of storageAfterReload.localKeys) {
      expect(key).not.toMatch(/token|auth|jwt|session|user/i)
    }

    // 3. Create VI positive (GOOD), EN negative (BAD), and VI neutral (MODERATE) posts
    const viPositive = `Hôm nay ứng dụng chạy rất mượt mà và tuyệt vời! #pos_${runId}`
    const enNegative = `Terrible release, buggy and completely broken. #neg_${runId}`
    const viNeutral = `Hôm nay mình đi họp lúc 9 giờ sáng tại văn phòng. #neu_${runId}`

    const createPost = async (text: string) => {
      await page.getByLabel('Share an update').fill(text)
      await page.getByRole('button', { name: /^Post$/ }).click()
      const card = page.locator('[data-testid="post-card"]').filter({ hasText: text })
      await expect(card).toBeVisible()
      return card
    }

    const posCard = await createPost(viPositive)
    await expect(posCard.getByTestId('sentiment-badge')).toHaveAttribute(
      'aria-label',
      'Sentiment: GOOD',
    )

    const negCard = await createPost(enNegative)
    await expect(negCard.getByTestId('sentiment-badge')).toHaveAttribute(
      'aria-label',
      'Sentiment: BAD',
    )

    const neuCard = await createPost(viNeutral)
    await expect(neuCard.getByTestId('sentiment-badge')).toHaveAttribute(
      'aria-label',
      'Sentiment: MODERATE',
    )

    // 4. Edit own post to negative Vietnamese text -> sentiment updates GOOD -> BAD
    const updatedViNegative = `Dịch vụ quá tệ và thật sự thất vọng. #edited_${runId}`
    await posCard.getByRole('button', { name: 'Edit post' }).click()
    const dialog = page.getByRole('dialog')
    await expect(dialog).toBeVisible()
    await dialog.getByLabel('Post content').fill(updatedViNegative)
    await dialog.getByRole('button', { name: 'Save changes' }).click()
    await expect(dialog).toBeHidden()

    const editedCard = page
      .locator('[data-testid="post-card"]')
      .filter({ hasText: updatedViNegative })
    await expect(editedCard).toBeVisible()
    await expect(editedCard.getByTestId('sentiment-badge')).toHaveAttribute(
      'aria-label',
      'Sentiment: BAD',
    )

    // 5. Search via q filter -> verify match & non-match filtered out -> clear search
    const searchBox = page.getByLabel('Search posts')
    await searchBox.fill(`#edited_${runId}`)
    await expect(editedCard).toBeVisible()
    await expect(
      page.locator('[data-testid="post-card"]').filter({ hasText: viNeutral }),
    ).toBeHidden()

    await page.getByRole('button', { name: 'Clear search' }).click()
    await expect(
      page.locator('[data-testid="post-card"]').filter({ hasText: viNeutral }),
    ).toBeVisible()

    // 6. Infinite scroll sentinel -> scrolling into view loads next page automatically with zero duplicate IDs
    const cardsBefore = page.locator('[data-testid="post-card"]')
    const initialCount = await cardsBefore.count()
    expect(initialCount).toBe(10)
    const sentinel = page.getByTestId('feed-scroll-sentinel')
    await expect(sentinel).toBeVisible()
    await sentinel.scrollIntoViewIfNeeded()

    await expect
      .poll(async () => await page.locator('[data-testid="post-card"]').count())
      .toBeGreaterThan(initialCount)

    const postIds = await page
      .locator('[data-testid="post-card"]')
      .evaluateAll((els) => els.map((el) => el.getAttribute('data-post-id')))
    expect(new Set(postIds).size).toBe(postIds.length)

    // 7. Delete an E2E post -> verify removed
    await editedCard.getByRole('button', { name: 'Delete post' }).click()
    const deleteDialog = page.getByRole('dialog')
    await expect(deleteDialog).toBeVisible()
    await deleteDialog.getByRole('button', { name: 'Confirm delete' }).click()
    await expect(deleteDialog).toBeHidden()
    await expect(editedCard).toBeHidden()

    // 8. Sign out -> redirected to /login; navigating to / redirects back to /login
    await page.getByRole('button', { name: 'Sign out' }).click()
    await expect(page).toHaveURL(/\/login$/)

    // Unauthenticated reload of / verifies the HttpOnly refresh cookie was revoked server-side
    isAuthenticated = false
    await page.goto('/')
    await expect(page).toHaveURL(/\/login$/)

    expect(errors).toEqual([])
  })

  test('login, forgot password via Mailpit, reset password, and sign in with new password', async ({
    page,
    baseURL,
  }) => {
    const errors: string[] = []
    let isAuthenticated = false
    let unauthRefreshHttp401 = 0
    let unauthRefreshConsole401 = 0

    page.on('pageerror', (err) => {
      errors.push(`pageerror: ${err.message}`)
    })
    page.on('response', (res) => {
      const status = res.status()
      if (status >= 500) {
        errors.push(`HTTP ${status} ${res.request().method()} ${res.url()}`)
        return
      }
      if (status >= 400) {
        const pathname = new URL(res.url()).pathname
        if (
          !isAuthenticated &&
          status === 401 &&
          res.request().method() === 'POST' &&
          pathname === '/api/auth/refresh' &&
          unauthRefreshHttp401 < 2
        ) {
          unauthRefreshHttp401 += 1
          return
        }
        errors.push(`Unexpected HTTP ${status} ${res.request().method()} ${res.url()}`)
      }
    })
    page.on('console', (msg) => {
      if (msg.type() !== 'error') return
      const locUrl = msg.location().url || ''
      const text = msg.text()
      if (
        !isAuthenticated &&
        locUrl.includes('/api/auth/refresh') &&
        text.includes('401') &&
        unauthRefreshConsole401 < 2
      ) {
        unauthRefreshConsole401 += 1
        return
      }
      errors.push(`console.error: ${text} (${locUrl})`)
    })

    const runId = `${Date.now()}_${Math.floor(Math.random() * 10000)}`
    const username = `e2e_pw_${runId}`
    const email = `${username}@example.com`
    const oldPassword = 'OldPassword123!'
    const newPassword = 'NewPassword456!'

    // Provision unique user via isolated API request context so browser cookies stay empty
    const apiCtx = await playwrightRequest.newContext({ baseURL })
    let resetToken = ''
    try {
      const regResp = await apiCtx.post('/api/auth/register', {
        data: {
          username,
          nickName: `Reset User ${runId}`,
          email,
          password: oldPassword,
        },
      })
      expect(regResp.status()).toBe(201)

      await page.goto('/login')
      await page.getByRole('button', { name: 'Forgot password?' }).click()
      const dialog = page.getByRole('dialog')
      await expect(dialog).toBeVisible()
      await dialog.getByLabel('Email address').fill(email)
      await dialog.getByRole('button', { name: 'Send reset link' }).click()
      await expect(dialog.getByRole('status')).toBeVisible()
      await dialog.getByRole('button', { name: 'Close', exact: true }).click()

      // Fetch reset token from local Mailpit HTTP API
      for (let attempt = 0; attempt < 20; attempt += 1) {
        const searchRes = await apiCtx.get(
          `http://127.0.0.1:8025/api/v1/search?query=${encodeURIComponent(`to:${email}`)}`,
        )
        if (searchRes.ok()) {
          const list = (await searchRes.json()) as { messages?: Array<{ ID: string }> }
          if (list.messages && list.messages.length > 0) {
            const msgRes = await apiCtx.get(
              `http://127.0.0.1:8025/api/v1/message/${list.messages[0].ID}`,
            )
            const detail = (await msgRes.json()) as { Text?: string }
            const match = detail.Text?.match(/token=([A-Za-z0-9_-]+)/)
            if (match?.[1]) {
              resetToken = match[1]
              break
            }
          }
        }
        await page.waitForTimeout(100)
      }
    } finally {
      await apiCtx.dispose()
    }
    expect(resetToken).not.toBe('')

    await page.goto(`/reset-password?token=${encodeURIComponent(resetToken)}`)
    await page.getByLabel(/^New password$/i).fill(newPassword)
    await page.getByLabel(/Confirm new password/i).fill(newPassword)
    await page.getByRole('button', { name: 'Reset password' }).click()
    await expect(page.getByRole('status')).toContainText('Password reset successfully')

    await page.getByRole('link', { name: 'sign in', exact: true }).click()
    await expect(page).toHaveURL(/\/login$/)

    await page.getByLabel('Username or Email').fill(username)
    await page.getByLabel('Password', { exact: true }).fill(newPassword)
    isAuthenticated = true
    await page.getByRole('button', { name: 'Sign in' }).click()

    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByLabel('Share an update')).toBeVisible()
    expect(errors).toEqual([])
  })

  test('two concurrent tabs in the same browser context coordinate refresh without 401 family revocation', async ({
    page,
    context,
  }) => {
    const errors: string[] = []
    let isAuthenticated = false
    let unauthRefreshHttp401 = 0
    let unauthRefreshConsole401 = 0

    const attachListeners = (target: typeof page, label: string) => {
      target.on('pageerror', (err) => {
        errors.push(`[${label}] pageerror: ${err.message}`)
      })
      target.on('response', (res) => {
        const status = res.status()
        if (status >= 500) {
          errors.push(`[${label}] HTTP ${status} ${res.request().method()} ${res.url()}`)
          return
        }
        if (status >= 400) {
          const pathname = new URL(res.url()).pathname
          if (
            !isAuthenticated &&
            status === 401 &&
            res.request().method() === 'POST' &&
            pathname === '/api/auth/refresh' &&
            unauthRefreshHttp401 < 2
          ) {
            unauthRefreshHttp401 += 1
            return
          }
          errors.push(`[${label}] Unexpected HTTP ${status} ${res.request().method()} ${res.url()}`)
        }
      })
      target.on('console', (msg) => {
        if (msg.type() !== 'error') return
        const locUrl = msg.location().url || ''
        const text = msg.text()
        if (
          !isAuthenticated &&
          locUrl.includes('/api/auth/refresh') &&
          text.includes('401') &&
          unauthRefreshConsole401 < 2
        ) {
          unauthRefreshConsole401 += 1
          return
        }
        errors.push(`[${label}] console.error: ${text} (${locUrl})`)
      })
    }

    attachListeners(page, 'tab1')

    const runId = `${Date.now()}_${Math.floor(Math.random() * 10000)}`
    const username = `e2e_tabs_${runId}`
    const email = `${username}@example.com`
    const password = 'Password123!'

    await page.goto('/register')
    await page.getByLabel('Username').fill(username)
    await page.getByLabel(/Display name/i).fill(`Tab User ${runId}`)
    await page.getByLabel('Email').fill(email)
    await page.getByLabel(/^Password$/i).fill(password)
    await page.getByLabel(/Confirm password/i).fill(password)

    isAuthenticated = true
    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByLabel('Share an update')).toBeVisible()

    const page2 = await context.newPage()
    attachListeners(page2, 'tab2')
    await page2.goto('/')
    await expect(page2).toHaveURL(/\/$/)
    await expect(page2.getByLabel('Share an update')).toBeVisible()

    // Concurrently reload both tabs twice — navigator.locks + BroadcastChannel prevent
    // submitting the same refresh JTI concurrently and triggering family revocation.
    for (let round = 0; round < 2; round += 1) {
      await Promise.all([page.reload(), page2.reload()])
      await expect(page).toHaveURL(/\/$/)
      await expect(page2).toHaveURL(/\/$/)
      await expect(page.getByLabel('Share an update')).toBeVisible()
      await expect(page2.getByLabel('Share an update')).toBeVisible()
    }

    await page2.close()
    expect(errors).toEqual([])
  })

  test('transient 503 during initial session restore preserves cookie and recovers on retry', async ({
    page,
  }) => {
    const errors: string[] = []
    let isAuthenticated = false
    let allowTransient503 = false
    let unauthRefreshHttp401 = 0
    let unauthRefreshConsole401 = 0

    page.on('pageerror', (err) => {
      errors.push(`pageerror: ${err.message}`)
    })
    page.on('response', (res) => {
      const status = res.status()
      const pathname = new URL(res.url()).pathname
      if (allowTransient503 && status === 503 && pathname === '/api/auth/refresh') {
        return
      }
      if (status >= 500) {
        errors.push(`HTTP ${status} ${res.request().method()} ${res.url()}`)
        return
      }
      if (status >= 400) {
        if (
          !isAuthenticated &&
          status === 401 &&
          res.request().method() === 'POST' &&
          pathname === '/api/auth/refresh' &&
          unauthRefreshHttp401 < 2
        ) {
          unauthRefreshHttp401 += 1
          return
        }
        errors.push(`Unexpected HTTP ${status} ${res.request().method()} ${res.url()}`)
      }
    })
    page.on('console', (msg) => {
      if (msg.type() !== 'error') return
      const locUrl = msg.location().url || ''
      const text = msg.text()
      if (allowTransient503 && locUrl.includes('/api/auth/refresh') && text.includes('503')) {
        return
      }
      if (
        !isAuthenticated &&
        locUrl.includes('/api/auth/refresh') &&
        text.includes('401') &&
        unauthRefreshConsole401 < 2
      ) {
        unauthRefreshConsole401 += 1
        return
      }
      errors.push(`console.error: ${text} (${locUrl})`)
    })

    const runId = `${Date.now()}_${Math.floor(Math.random() * 10000)}`
    const username = `e2e_503_${runId}`
    const email = `${username}@example.com`
    const password = 'Password123!'

    await page.goto('/register')
    await page.getByLabel('Username').fill(username)
    await page.getByLabel(/Display name/i).fill(`Recovery User ${runId}`)
    await page.getByLabel('Email').fill(email)
    await page.getByLabel(/^Password$/i).fill(password)
    await page.getByLabel(/Confirm password/i).fill(password)

    isAuthenticated = true
    await page.getByRole('button', { name: 'Create account' }).click()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByLabel('Share an update')).toBeVisible()

    // Simulate two transient 503 failures during initial bootstrap refresh on reload
    let refresh503Attempts = 0
    allowTransient503 = true
    await page.route('**/api/auth/refresh', async (route) => {
      refresh503Attempts += 1
      await route.fulfill({
        status: 503,
        contentType: 'application/json',
        body: JSON.stringify({ error: 'SERVICE_UNAVAILABLE', message: 'transient outage' }),
      })
    })

    await page.reload()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByTestId('session-restore-recovery')).toBeVisible()
    expect(refresh503Attempts).toBe(2)

    // Backend recovers -> unroute and click Retry session restore -> recovers without redirecting to /login
    await page.unroute('**/api/auth/refresh')
    allowTransient503 = false
    await page.getByRole('button', { name: 'Retry session restore' }).click()

    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByLabel('Share an update')).toBeVisible()

    // Simulate a 200 response whose body stream fails after 200 headers arrive -> retries twice,
    // shows recovery UI instead of redirecting to /login, and recovers on manual retry.
    await page.addInitScript(() => {
      const origFetch = window.fetch.bind(window)
      window.fetch = async (input: RequestInfo | URL, init?: RequestInit) => {
        const url = typeof input === 'string' ? input : input instanceof URL ? input.toString() : input.url
        if (url.includes('/api/auth/refresh') && window.name.startsWith('SIMULATE_STREAM_FAIL:')) {
          const count = Number(window.name.split(':')[1] || '0') + 1
          window.name = `SIMULATE_STREAM_FAIL:${count}`
          const failingStream = new ReadableStream({
            pull(controller) {
              controller.error(new TypeError('simulated 200 body stream failure'))
            },
          })
          return new Response(failingStream, {
            status: 200,
            headers: { 'Content-Type': 'application/json' },
          })
        }
        return origFetch(input, init)
      }
    })

    await page.evaluate(() => {
      window.name = 'SIMULATE_STREAM_FAIL:0'
    })
    await page.reload()
    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByTestId('session-restore-recovery')).toBeVisible()
    const streamAttempts = await page.evaluate(() => Number(window.name.split(':')[1] || '0'))
    expect(streamAttempts).toBe(2)

    await page.evaluate(() => {
      window.name = ''
    })
    await page.getByRole('button', { name: 'Retry session restore' }).click()

    await expect(page).toHaveURL(/\/$/)
    await expect(page.getByLabel('Share an update')).toBeVisible()
    expect(errors).toEqual([])
  })
})
