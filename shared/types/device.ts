export interface Device {
  id: string
  name: string
  mac: string
  address: string | null
  sshUser: string | null
  remoteMethod: RemoteMethod
  companionConfigured: boolean
  createdAt: string
  updatedAt: string
  lastSentAt: string | null
}

export type RemoteMethod = 'ssh' | 'companion' | 'none'

export interface DeviceInput {
  name: string
  mac: string
  address: string | null
  remoteMethod?: RemoteMethod
  sshUser: string | null
  companionCode?: string
}

export interface WakeResult {
  message: string
  device: Device
  retryAfter: number
}

export interface DeviceStatus {
  deviceId: string
  networkReachable: boolean
  remoteReady: boolean
  remoteMethod: RemoteMethod
  checkedAt: string
}

export interface ShutdownResult {
  message: string
  retryAfter: number
}
