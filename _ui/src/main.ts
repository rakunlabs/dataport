import '@fontsource-variable/inter';
import '@fontsource-variable/jetbrains-mono';
import '@/style/global.css';
import '@/lib/stores/theme.svelte';
import { ready } from '@/lib/i18n/index.svelte';
import App from '@/App.svelte';
import { mount } from 'svelte';

// Wait for the locale so the first paint is already translated.
export default ready.then(() => mount(App, { target: document.body }));
