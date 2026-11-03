import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// The build lands where the Go binary embeds it (internal/ui). In
// development, `npm run dev` serves the app on :5173 and sends everything
// else to the Go server on :8080, so sign in at localhost:5173/login.
export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/ui/dist",
    // The directory keeps a committed .keep so the Go build works before
    // the UI is built; package.json's build script clears the old output.
    emptyOutDir: false,
  },
  server: {
    proxy: {
      "/api": "http://localhost:8080",
      "/login": "http://localhost:8080",
      "/logout": "http://localhost:8080",
      "/play": "http://localhost:8080",
      "/nodes": "http://localhost:8080",
      "/choices": "http://localhost:8080",
      "/stages": "http://localhost:8080",
    },
  },
});
