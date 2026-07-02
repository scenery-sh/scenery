import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { astryxStylex } from '@astryxdesign/build/vite'

// https://vite.dev/config/
export default defineConfig({
  plugins: [...astryxStylex(), react()],
})
