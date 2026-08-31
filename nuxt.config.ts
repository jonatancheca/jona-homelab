export default defineNuxtConfig({
  compatibilityDate: '2026-08-31',
  ssr: false,
  devtools: { enabled: false },
  devServer: { host: '127.0.0.1', port: 3000 },
  hooks: {
    listen(server) {
      const address = server.address()
      if (!address || typeof address === 'string' || address.address !== '127.0.0.1') {
        server.close()
        throw new Error('The development server must listen only on 127.0.0.1.')
      }
    },
  },
  modules: ['@nuxt/eslint'],
  css: ['~/assets/main.css'],
  nitro: { preset: 'node-server', rollupConfig: { external: ['node:sqlite'] } },
  app: {
    head: {
      title: 'Jona Homelab · Wake-on-LAN',
      htmlAttrs: { lang: 'en' },
      meta: [
        { name: 'description', content: 'Your network, one tap away. Private Wake-on-LAN panel.' },
        { name: 'robots', content: 'noindex, nofollow' },
        { name: 'theme-color', content: '#f6f7f9' },
      ],
      link: [{ rel: 'icon', type: 'image/svg+xml', href: '/favicon.svg' }],
    },
  },
  typescript: { strict: true },
})
