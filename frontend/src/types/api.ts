export type UserRole = 'USER' | 'ADMIN'

export type Sentiment = 'GOOD' | 'MODERATE' | 'BAD'

export interface PublicUser {
  id: string
  userName: string
  nickName: string
  email: string
  role: UserRole
  avatarUrl: string | null
  coverPageUrl: string | null
  bio: string | null
  dateOfBirth: string | null
  isActive: boolean
  createdAt: string
  lastLoginAt: string | null
}

export interface AuthResponse {
  accessToken: string
  user: PublicUser
}

export interface PostDTO {
  id: string
  content: string
  userId: string
  sentiment: Sentiment
  contentVersion: number
  createdAt: string
  updatedAt: string
  author: ProfileSummary
  groupId?: string
  likeCount: number
  commentCount: number
  bookmarkCount: number
  liked: boolean
  bookmarked: boolean
  attachments: MediaAttachment[]
}

export interface MediaAttachment {
  mediaId: string
  mimeType: string
}

export type MediaVisibility = 'PUBLIC' | 'PRIVATE'

export interface ProfileSummary {
  id: string
  userName: string
  nickName: string
  avatarUrl: string | null
  bio?: string | null
  dateOfBirth?: string | null
}

export interface Profile extends ProfileSummary {
  coverPageUrl: string | null
  followerCount: number
  followingCount: number
  following: boolean
}

export interface CommentDTO {
  id: string
  postId: string
  content: string
  sentiment: Sentiment
  contentVersion: number
  createdAt: string
  author: ProfileSummary
  attachments: MediaAttachment[]
}

export interface InteractionState {
  liked: boolean
  bookmarked: boolean
  likeCount: number
  bookmarkCount: number
}

export interface Bookmark {
  id: string
  postId: string
  content: string
  sentiment: Sentiment
  authorName: string
  createdAt: string
}

export interface Group {
  id: string
  creatorId: string
  name: string
  description: string | null
  avatarMediaId: string | null
  memberCount: number
  viewerRole: string
  viewerStatus: string
}

export interface CommunityEvent {
  id: string
  creatorId: string
  name: string
  description: string
  category: string
  address: string | null
  eventDate: string
  avatarMediaId: string | null
  attendeeCount: number
  viewerRole: string
  viewerStatus: string
}

export interface ChatMessage {
  id: string
  roomId: string
  roomSeq: number
  senderId?: string
  content?: string
  createdAt?: string
  deleted?: boolean
  attachments?: MediaAttachment[]
}

export interface ChatRoom {
  id: string
  name: string
  type: 'DIRECT' | 'GROUP'
  lastSeq: number
  lastReadSeq: number
  lastMessage: { id: string; content: string; roomSeq: number } | null
  unreadCount: number
  participants: ProfileSummary[]
}

export interface Notification {
  id: string
  userNotifSeq: number
  type?: string
  content?: string
  senderId?: string
  entityId?: string
  createdAt?: string
  isRead?: boolean
  deleted?: boolean
}

export interface CursorPage<T> {
  data: T[]
  nextCursor: string | null
}

export interface SequencePage<T> {
  data: T[]
  hasMore: boolean
}

export interface FeedResponse {
  data: PostDTO[]
  nextCursor: string | null
  hasMore: boolean
}

export interface MessageResponse {
  message: string
}

export interface ApiErrorPayload {
  error?: string
  message?: string
}
