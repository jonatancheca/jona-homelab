import { test } from 'node:test'
import assert from 'node:assert/strict'
import { createServer, request } from 'node:http'
import { createApp, toNodeListener } from 'h3'
import { apiHandler, readJson } from '../../server/utils/http.ts'

test('streaming JSON supports proxy chunking and enforces a real byte limit', async () => {
  const app = createApp().use(apiHandler(async event => ({ body: await readJson(event) })))
  const server = createServer(toNodeListener(app))
  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const address = server.address()
  assert.ok(address && typeof address === 'object')
  const send = (chunks: string[]) => new Promise<{ status: number, data: string }>((resolve, reject) => {
    const outgoing = request({ host: '127.0.0.1', port: address.port, method: 'POST', headers: { 'content-type': 'application/json', 'transfer-encoding': 'chunked' } }, (incoming) => {
      let data = ''
      incoming.on('data', chunk => { data += chunk.toString() })
      incoming.on('end', () => resolve({ status: incoming.statusCode!, data }))
    })
    outgoing.on('error', reject)
    for (const chunk of chunks) outgoing.write(chunk)
    outgoing.end()
  })
  try {
    const valid = await send(['{"name":', '"prueba"}'])
    assert.equal(valid.status, 200)
    assert.deepEqual(JSON.parse(valid.data), { body: { name: 'prueba' } })
    assert.equal((await send(['{"name":"', 'x'.repeat(5000), '"}'])).status, 413)
    assert.equal((await send(['{bad}'])).status, 400)
  }
  finally { await new Promise<void>((resolve, reject) => server.close(error => error ? reject(error) : resolve())) }
})
