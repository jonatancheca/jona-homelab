import withNuxt from './.nuxt/eslint.config.mjs'

export default withNuxt({
  ignores: ['artifacts/**', 'test-results/**', 'playwright-report/**'],
}, {
  rules: {
    '@typescript-eslint/no-explicit-any': 'error',
    'vue/multi-word-component-names': 'off',
    'vue/html-self-closing': ['error', { html: { void: 'always', normal: 'never', component: 'always' } }],
  },
})
