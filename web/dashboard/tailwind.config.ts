import type { Config } from "tailwindcss";

// Gubern dashboard Tailwind config. Colours come from CSS variables in
// src/index.css so the light and dark themes swap by class on <html>.
const v = (name: string) => `hsl(var(--${name}) / <alpha-value>)`;

const config: Config = {
  darkMode: "class",
  content: ["./index.html", "./src/**/*.{ts,tsx}"],
  theme: {
    extend: {
      colors: {
        border: v("border"),
        background: v("background"),
        rail: v("rail"),
        surface: v("surface"),
        "surface-2": v("surface-2"),
        foreground: v("foreground"),
        card: v("card"),
        "card-foreground": v("card-foreground"),
        muted: v("muted"),
        "muted-foreground": v("muted-foreground"),
        accent: v("accent"),
        selected: v("selected"),
        primary: v("primary"),
        "primary-foreground": v("primary-foreground"),
        success: v("success"),
        warning: v("warning"),
        danger: v("danger"),
        ai: v("ai"),
        "ai-foreground": v("ai-foreground"),
        ring: v("ring"),
      },
      fontFamily: {
        sans: ['"Fira Sans"', "ui-sans-serif", "system-ui", "sans-serif"],
        mono: ['"Fira Code"', "ui-monospace", "SFMono-Regular", "monospace"],
      },
      borderRadius: {
        DEFAULT: "8px",
        lg: "10px",
        xl: "14px",
      },
      zIndex: {
        rail: "20",
        panel: "30",
        overlay: "40",
        modal: "50",
        toast: "60",
      },
      keyframes: {
        "fade-in": { from: { opacity: "0" }, to: { opacity: "1" } },
        flash: { "0%": { backgroundColor: "hsl(var(--primary) / 0.18)" }, "100%": { backgroundColor: "transparent" } },
      },
      animation: {
        "fade-in": "fade-in 150ms ease-out",
        flash: "flash 1.2s ease-out",
      },
    },
  },
  plugins: [],
};

export default config;
