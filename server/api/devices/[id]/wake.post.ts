import { wakeDevice } from '../../../core/service.ts'
import { sendMagicPacket } from '../../../core/wol.ts'
import { getRuntime } from '../../../utils/runtime'
import { apiHandler, deviceId } from '../../../utils/http'

export default apiHandler((event) => {
  const { store, settings } = getRuntime()
  return wakeDevice(store, deviceId(event), mac => sendMagicPacket(mac, settings.wol))
})
