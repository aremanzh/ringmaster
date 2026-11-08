<?php
// Simulated `php artisan queue:work` for the Ringmaster demo: prints a job
// every few seconds until stopped. Ringmaster timestamps each log line, so
// the worker prints none of its own.
$jobs = ['App\Jobs\SendInvoice', 'App\Jobs\ResizeImage', 'App\Jobs\SyncInventory'];
echo "   INFO  Processing jobs from the [default] queue on PHP " . PHP_VERSION . ".\n\n";
for ($i = 0; ; $i++) {
    sleep(4);
    $job = $jobs[$i % count($jobs)];
    $dots = str_repeat('.', 40 - strlen($job));
    echo "  $job $dots RUNNING\n";
    usleep(300000);
    echo "  $job $dots 300ms DONE\n";
}
