import { getRouterParam } from 'h3'
import { parseFavoriteId, parseFavoriteInput } from '../../core/favorites.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler, readJson } from '../../utils/http'

export default apiHandler(async event => getRuntime().store.updateFavorite(
  parseFavoriteId(getRouterParam(event, 'id')),
  parseFavoriteInput(await readJson(event)),
))
