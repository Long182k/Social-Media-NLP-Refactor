import { apiFetch } from '@/lib/api'
import { readFeed, readPost } from '@/lib/graphApi'
import type { MessageResponse, PostDTO } from '@/types/api'

export interface ListPostsParams {
  cursor?: string
  limit?: number
  q?: string
}

export const postsApi = {
  listPosts: readFeed,

  createPost(payload: { content: string; mediaIds?: string[] }): Promise<PostDTO> {
    return apiFetch<PostDTO>('/api/posts', {
      method: 'POST',
      body: JSON.stringify(payload),
    })
  },

  getPost: readPost,

  updatePost(id: string, payload: { content: string; mediaIds?: string[] }): Promise<PostDTO> {
    return apiFetch<PostDTO>(`/api/posts/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(payload),
    })
  },

  deletePost(id: string): Promise<MessageResponse> {
    return apiFetch<MessageResponse>(`/api/posts/${encodeURIComponent(id)}`, {
      method: 'DELETE',
    })
  },
}
