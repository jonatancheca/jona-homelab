import { parseDeviceInput } from '../../core/validation.ts'
import { getRuntime } from '../../utils/runtime'
import { apiHandler, deviceId, readJson } from '../../utils/http'

export default apiHandler(async event => getRuntime().store.update(deviceId(event), parseDeviceInput(await readJson(event))))
