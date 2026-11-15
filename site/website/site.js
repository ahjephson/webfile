const toggle = document.querySelector('.menu-toggle');
const navigation = document.querySelector('#primary-nav');

function closeMenu() {
  toggle.setAttribute('aria-expanded', 'false');
  navigation.classList.remove('is-open');
}

toggle.addEventListener('click', () => {
  const expanded = toggle.getAttribute('aria-expanded') !== 'true';
  toggle.setAttribute('aria-expanded', String(expanded));
  navigation.classList.toggle('is-open', expanded);
});

navigation.addEventListener('click', event => {
  if (event.target.closest('a')) closeMenu();
});

document.addEventListener('keydown', event => {
  if (event.key === 'Escape' && toggle.getAttribute('aria-expanded') === 'true') {
    closeMenu();
    toggle.focus();
  }
});

const copyButton = document.querySelector('.copy-button');
const copyStatus = document.querySelector('#copy-status');

copyButton.addEventListener('click', async () => {
  const command = document.querySelector('#version-command').textContent;
  try {
    await navigator.clipboard.writeText(command);
    copyButton.textContent = 'Copied';
    copyStatus.textContent = 'Command copied to clipboard.';
  } catch {
    const selection = window.getSelection();
    const range = document.createRange();
    range.selectNodeContents(document.querySelector('#version-command'));
    selection.removeAllRanges();
    selection.addRange(range);
    copyStatus.textContent = 'Command selected. Press Ctrl+C or Cmd+C to copy.';
  }
});
