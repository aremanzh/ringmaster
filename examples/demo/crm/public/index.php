<?php // Demo page for the Ringmaster "crm" project. ?>
<!doctype html>
<html lang="en">
<head><meta charset="utf-8"><title>CRM (demo)</title></head>
<body style="font-family: system-ui, sans-serif; margin: 48px; background: #F7F5F0; color: #16181D">
  <h1>CRM</h1>
  <p>A demo project served by <code>php -S</code> under Ringmaster.</p>
  <p>PHP <?= PHP_VERSION ?>, port <?= $_SERVER['SERVER_PORT'] ?>, <?= date('H:i:s') ?></p>
</body>
</html>
