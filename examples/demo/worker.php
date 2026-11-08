<?php
// Simulated `php artisan queue:work` for the Ringmaster demo: prints a job
// every few seconds until stopped.
$jobs = ['App\Jobs\SendInvoice', 'App\Jobs\ResizeImage', 'App\Jobs\SyncInventory'];
echo "   INFO  Processing jobs from the [default] queue.\n\n";
for ($i = 0; ; $i++) {
    sleep(4);
    $job = $jobs[$i % count($jobs)];
    printf("  %s %s %s RUNNING\n", date('Y-m-d H:i:s'), $job, str_repeat('.', 40 - strlen($job)));
    usleep(300000);
    printf("  %s %s %s 300ms DONE\n", date('Y-m-d H:i:s'), $job, str_repeat('.', 40 - strlen($job)));
}
