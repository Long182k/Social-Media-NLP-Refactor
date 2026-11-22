import { ClientError, GraphQLClient, type Variables } from 'graphql-request'
import { ApiError, authorizedFetch } from '@/lib/api'
import type { FeedResponse, Group, PostDTO, Profile } from '@/types/api'

const endpoint = new URL('/graphql', window.location.origin).href
const client = new GraphQLClient(endpoint, { fetch: (_input, init) => authorizedFetch('/graphql', init) })

export async function queryGraph<T>(document: string, variables?: Variables): Promise<T> {
  try {
    return await client.request<T>(document, variables)
  } catch (error) {
    if (error instanceof ClientError) {
      const first = error.response.errors?.[0]
      throw new ApiError(error.response.status, String(first?.extensions?.code ?? 'GRAPHQL_ERROR'), first?.message ?? 'Unable to load data.')
    }
    throw error
  }
}

type Counts = Pick<PostDTO, 'likeCount' | 'bookmarkCount' | 'commentCount'>
type Viewer = Pick<PostDTO, 'liked' | 'bookmarked'>
type GraphPost = Omit<PostDTO, keyof Counts | keyof Viewer> & { counts: Counts; viewerInteractions: Viewer }
type GraphFeed = Omit<FeedResponse, 'data'> & { data: GraphPost[] }

const postFields = `
  id userId groupId content sentiment contentVersion createdAt updatedAt
  author { id userName nickName avatarUrl }
  attachments { mediaId mimeType }
  counts { likeCount bookmarkCount commentCount }
  viewerInteractions { liked bookmarked }
`
const pageFields = `data { ${postFields} } nextCursor hasMore`

function flattenPost({ counts, viewerInteractions, ...post }: GraphPost): PostDTO {
  return { ...post, ...counts, ...viewerInteractions }
}

function flattenFeed(feed: GraphFeed): FeedResponse {
  return { ...feed, data: feed.data.map(flattenPost) }
}

function requireResult<T>(data: T | null, label: string): T {
  if (data === null) throw new ApiError(404, 'NOT_FOUND', `${label} not found.`)
  return data
}

export async function readFeed(params: { cursor?: string; limit?: number; q?: string } = {}) {
  const { feed } = await queryGraph<{ feed: GraphFeed }>(
    `query Feed($after: String, $first: Int, $search: String) {
      feed(after: $after, first: $first, search: $search) { ${pageFields} }
    }`, { after: params.cursor, first: params.limit, search: params.q },
  )
  return flattenFeed(feed)
}

export async function readPost(id: string) {
  const { post } = await queryGraph<{ post: GraphPost | null }>(
    `query Post($id: UUID!) { post(id: $id) { ${postFields} } }`, { id },
  )
  return flattenPost(requireResult(post, 'Post'))
}

const profileFields = `
  id userName nickName avatarUrl coverPageUrl bio dateOfBirth followerCount followingCount following
`
const groupFields = `id creatorId name description avatarMediaId memberCount viewerRole viewerStatus`

export async function readProfile(id: string) {
  const { userProfile } = await queryGraph<{ userProfile: Profile | null }>(
    `query Profile($id: UUID!) { userProfile(id: $id) { ${profileFields} } }`, { id },
  )
  return requireResult(userProfile, 'Profile')
}

export async function readGroup(id: string) {
  const { groupDetail } = await queryGraph<{ groupDetail: Group | null }>(
    `query Group($id: UUID!) { groupDetail(id: $id) { ${groupFields} } }`, { id },
  )
  return requireResult(groupDetail, 'Group')
}

export async function readScopedFeed(scope: 'userProfile' | 'groupDetail', id: string, after?: string) {
  const result = await queryGraph<Record<typeof scope, { posts: GraphFeed } | null>>(
    `query ScopeFeed($id: UUID!, $after: String) {
      ${scope}(id: $id) { posts(after: $after) { ${pageFields} } }
    }`, { id, after },
  )
  return flattenFeed(requireResult(result[scope], scope === 'userProfile' ? 'Profile' : 'Group').posts)
}
