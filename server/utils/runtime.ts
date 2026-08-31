import { readSettings, type Settings } from '../core/config.ts'
import { DeviceStore } from '../core/database.ts'
import { AppError } from '../core/errors.ts'

interface Runtime {
  settings: Settings
  store: DeviceStore
}

let runtime: Runtime | undefined

export function initializeRuntime(development: boolean): Runtime {
  const settings = readSettings(process.env, development)
  const store = new DeviceStore(settings.databasePath)
  runtime = { settings, store }
  return runtime
}

export function getRuntime(): Runtime {
  if (!runtime) throw new AppError(503, 'Service unavailable.')
  return runtime
}
