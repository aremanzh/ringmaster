// Simulated Vite dev server for the Ringmaster demo. It prints the lines a
// real `npm run dev` prints, opens no port, and stays alive until stopped.
const started = Date.now();
setTimeout(() => {
  console.log(`\n  VITE v5.4.21  ready in ${Date.now() - started + 280} ms\n`);
  console.log('  ➜  Local:   http://localhost:5173/ (simulated)');
  console.log('\n  LARAVEL v11  plugin v1.0.6\n');
}, 300);

const files = ['resources/js/app.js', 'resources/css/app.css', 'resources/views/welcome.blade.php'];
let i = 0;
setInterval(() => {
  console.log(`${new Date().toLocaleTimeString()} [vite] hmr update /${files[i++ % files.length]}`);
}, 7000);
