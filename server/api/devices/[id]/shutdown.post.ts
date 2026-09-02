import { sendShutdownCommand } from '../../../core/remote.ts'
import { shutdownDevice } from '../../../core/service.ts'
import { parseShutdownInput } from '../../../core/validation.ts'
import { getRuntime } from '../../../utils/runtime'
import { apiHandler, deviceId, readJson } from '../../../utils/http'

export default apiHandler(async (event) => {
  const { store, settings } = getRuntime()
  const { force } = parseShutdownInput(await readJson(event))
  return shutdownDevice(store, deviceId(event), force, (device, shouldForce) => sendShutdownCommand(device, shouldForce, settings.ssh))
})
