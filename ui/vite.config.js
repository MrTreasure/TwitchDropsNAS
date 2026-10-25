import { defineConfig } from 'vite';
export default defineConfig({
 build: {
  rolldownOptions: {
   onwarn(warning, defaultHandler) {
    // This is a browser-only app; React Server Component directives do not apply.
    if (warning.code === 'MODULE_LEVEL_DIRECTIVE' && warning.message.includes('use client')) return;
    defaultHandler(warning);
   },
  },
 },
});
