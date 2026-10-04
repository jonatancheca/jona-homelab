export interface FavoriteInput {
  name: string
  url: string
}

export interface Favorite extends FavoriteInput {
  id: string
  createdAt: string
  updatedAt: string
}
