import { setResponseStatus } from 'h3'
import { parseFavoriteInput } from '../../core/favorites.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler, readJson } from '../../utils/http'

export default apiHandler(async (event) => {
  const favorite = getRuntime().store.createFavorite(parseFavoriteInput(await readJson(event)))
  setResponseStatus(event, 201)
  return favorite
})
