// Restore an explicit preference before the stylesheet's first paint.
(() => {
  const root = document.documentElement;
  const key = 'walter-theme';
  const valid = value => value === 'light' || value === 'dark';
  function apply(value) {
    if (valid(value)) root.dataset.theme = value;
    else delete root.dataset.theme;
    document.querySelectorAll('meta[name="theme-color"]').forEach(meta => {
      const dark = value === 'dark' || (!valid(value) && meta.media.includes('dark'));
      meta.content = dark ? '#121420' : '#f7f8fb';
    });
  }
  let preference = 'system';
  try {
    const stored = localStorage.getItem(key);
    if (valid(stored)) preference = stored;
  } catch { /* Storage can be unavailable in private browser contexts. */ }
  apply(preference);
  document.addEventListener('DOMContentLoaded', () => {
    const select = document.getElementById('theme');
    select.value = preference;
    select.addEventListener('change', () => {
      preference = valid(select.value) ? select.value : 'system';
      apply(preference);
      try {
        if (preference === 'system') localStorage.removeItem(key);
        else localStorage.setItem(key, preference);
      } catch { /* The explicit choice still applies to this page. */ }
      const picker = select.closest('details');
      picker.open = false;
      picker.querySelector('summary').focus();
    });
  });
})();
