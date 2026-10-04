import { getRouterParam, setResponseStatus } from 'h3'
import { parseFavoriteId } from '../../core/favorites.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler } from '../../utils/http'

export default apiHandler((event) => {
  getRuntime().store.deleteFavorite(parseFavoriteId(getRouterParam(event, 'id')))
  setResponseStatus(event, 204)
  return null
})
