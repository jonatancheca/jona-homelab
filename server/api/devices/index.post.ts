import { setResponseStatus } from 'h3'
import { parseDeviceInput } from '../../core/validation.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler, readJson } from '../../utils/http'

export default apiHandler(async (event) => {
  const { store } = getRuntime()
  const device = store.create(parseDeviceInput(await readJson(event)))
  setResponseStatus(event, 201)
  return device
})
