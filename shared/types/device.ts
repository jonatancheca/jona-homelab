export interface Device {
  id: string
  name: string
  mac: string
  createdAt: string
  updatedAt: string
  lastSentAt: string | null
}

export interface DeviceInput {
  name: string
  mac: string
}

export interface WakeResult {
  message: string
  device: Device
  retryAfter: number
}
