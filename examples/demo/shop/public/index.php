<?php // Demo page for the Ringmaster "shop" project. Plain PHP, no framework. ?>
<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>Shop · PHP <?= PHP_MAJOR_VERSION . '.' . PHP_MINOR_VERSION ?></title>
<style>
  body { font-family: system-ui, sans-serif; margin: 0; min-height: 100vh; display: grid; place-items: center; background: #F7F5F0; color: #16181D; }
  main { text-align: center; }
  h1 { font-size: 28px; font-weight: 600; margin: 0 0 8px; letter-spacing: .02em; }
  .ver { font-size: 120px; font-weight: 800; line-height: 1; color: #E8A33D; margin: 0; }
  .ver small { display: block; font-size: 22px; font-weight: 500; color: #6B6F78; margin-top: 14px; }
  .bin { font: 14px/1.4 "Cascadia Code", Consolas, monospace; color: #6B6F78; margin: 10px 24px 0; word-break: break-all; }
  p { color: #6B6F78; margin: 24px 0 0; }
  code { background: #ECE9E1; padding: 2px 6px; border-radius: 4px; }
</style>
</head>
<body>
<main>
  <h1>Shop</h1>
  <p class="ver">PHP <?= PHP_MAJOR_VERSION . '.' . PHP_MINOR_VERSION ?><small><?= PHP_VERSION ?></small></p>
  <p class="bin"><?= htmlspecialchars(PHP_BINARY) ?></p>
  <p>Served on port <code><?= (int) $_SERVER['SERVER_PORT'] ?></code>, alongside the other demo projects.</p>
</main>
</body>
</html>
