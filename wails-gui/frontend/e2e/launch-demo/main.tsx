// Launch demo harness: the production App over hard-coded sample content.
// URL params: theme=onedark|paper.
import { installFixture } from './fixture';

const theme = new URLSearchParams(location.search).get('theme') === 'paper' ? 'paper' : 'onedark';
localStorage.setItem('yansu.theme', theme);
document.documentElement.dataset.theme = theme;
installFixture();
const { mount } = await import('./mount');
mount();
