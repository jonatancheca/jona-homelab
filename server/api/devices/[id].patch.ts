import { parseDeviceInput } from '../../core/validation.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler, deviceId, readJson } from '../../utils/http'

export default apiHandler(async event => {
  const { store } = getRuntime()
  const id = deviceId(event)
  return store.update(id, parseDeviceInput(await readJson(event), store.get(id)))
})
