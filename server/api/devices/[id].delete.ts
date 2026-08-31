import { setResponseStatus } from 'h3'
import { getRuntime } from '../../utils/runtime'
import { apiHandler, deviceId } from '../../utils/http'

export default apiHandler((event) => {
  getRuntime().store.delete(deviceId(event))
  setResponseStatus(event, 204)
  return null
})
