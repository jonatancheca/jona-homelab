import { updateRemoteCompanion } from '../../../core/companion-updates.ts'
import { AppError } from '../../../core/errors.ts'
import { getRuntime } from '../../../utils/runtime'
import { apiHandler, deviceId, readJson } from '../../../utils/http'

export default apiHandler(async (event) => {
  const body = await readJson(event)
  if (!body || typeof body !== 'object' || Array.isArray(body) || Object.keys(body).length) throw new AppError(400, 'La actualización no admite parámetros.')
  const { store } = getRuntime()
  const device = store.get(deviceId(event))
  if (device.remoteMethod !== 'companion') throw new AppError(409, 'Este dispositivo no usa Companion.')
  return updateRemoteCompanion(device, store.companionSecret(device.id))
})
