import { sendPowerCommand } from '../../../core/remote.ts'
import { powerDevice } from '../../../core/service.ts'
import { parsePowerInput } from '../../../core/validation.ts'
import { getRuntime } from '../../../utils/runtime'
import { apiHandler, deviceId, readJson } from '../../../utils/http'

export default apiHandler(async (event) => {
  const { store, settings } = getRuntime()
  const input = parsePowerInput(await readJson(event))
  return powerDevice(store, deviceId(event), input, (device, action) => sendPowerCommand(
    device, action, settings.ssh,
    device.remoteMethod === 'companion' ? store.companionSecret(device.id) : undefined,
  ))
})
