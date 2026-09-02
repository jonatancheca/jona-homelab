export interface Device {
  id: string
  name: string
  mac: string
  address: string | null
  sshUser: string | null
  createdAt: string
  updatedAt: string
  lastSentAt: string | null
}

export interface DeviceInput {
  name: string
  mac: string
  address: string
  sshUser: string
}

export interface WakeResult {
  message: string
  device: Device
  retryAfter: number
}

export interface DeviceStatus {
  deviceId: string
  networkReachable: boolean
  sshReady: boolean
  checkedAt: string
}

export interface ShutdownResult {
  message: string
  retryAfter: number
}
