package ir.schayan.messenger;

import android.app.Notification;
import android.app.NotificationChannel;
import android.app.NotificationManager;
import android.app.PendingIntent;
import android.app.Service;
import android.content.Context;
import android.content.Intent;
import android.content.pm.ServiceInfo;
import android.os.Build;
import android.os.IBinder;

// LiveService keeps one request open to the messenger's own server, so a call
// rings and a message arrives within seconds while the app is closed (asked
// for on 2026-10-19: the quarter-hourly check could never catch a call that
// rings for 45 seconds). No push service is involved: Google's is abroad, and
// gone when the international link is cut. The server holds each request until
// something is new or 50 seconds pass (POST /api/app/poll with "wait").
// Android requires the notification below for as long as it runs; it can be
// hidden from the phone's notification settings without stopping anything.
public class LiveService extends Service {
    static volatile boolean running;
    static volatile long lastOk;
    private static final String CHANNEL = "live";
    private Thread loop;

    static void start(Context c) {
        Intent i = new Intent(c, LiveService.class);
        try {
            if (Build.VERSION.SDK_INT >= 26) c.startForegroundService(i);
            else c.startService(i);
        } catch (RuntimeException e) {
            // Not allowed from the background just now; the job tries again.
        }
    }

    /** connected: a check got through in the last minute and a half. */
    static boolean connected() {
        return running && System.currentTimeMillis() - lastOk < 90_000;
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (Build.VERSION.SDK_INT >= 26) {
            NotificationChannel ch = new NotificationChannel(CHANNEL, getString(R.string.live_channel), NotificationManager.IMPORTANCE_MIN);
            ch.setShowBadge(false);
            nm.createNotificationChannel(ch);
        }
        Intent back = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
        PendingIntent open = PendingIntent.getActivity(this, 3, back, PendingIntent.FLAG_IMMUTABLE);
        Notification.Builder b = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(this, CHANNEL) : new Notification.Builder(this);
        Notification n = b.setContentTitle(getString(R.string.live_title))
                .setContentText(getString(R.string.live_text))
                .setSmallIcon(android.R.drawable.stat_notify_chat)
                .setContentIntent(open)
                .setOngoing(true)
                .build();
        try {
            if (Build.VERSION.SDK_INT >= 34) startForeground(4, n, ServiceInfo.FOREGROUND_SERVICE_TYPE_SPECIAL_USE);
            else startForeground(4, n);
        } catch (RuntimeException e) {
            stopSelf();
            return START_NOT_STICKY;
        }
        running = true;
        if (loop == null || !loop.isAlive()) {
            loop = new Thread(this::run, "messenger-live");
            loop.start();
        }
        return START_STICKY;
    }

    private void run() {
        long backoff = 2_000;
        while (running) {
            long t0 = System.currentTimeMillis();
            if (NotifyJob.poll(getApplicationContext(), true, 50)) {
                lastOk = System.currentTimeMillis();
                backoff = 2_000;
                // Never more than one request in two seconds, even from a
                // server (or a proxy) that answers without holding it.
                long took = lastOk - t0;
                if (took < 2_000) {
                    try {
                        Thread.sleep(2_000 - took);
                    } catch (InterruptedException e) {
                        return;
                    }
                }
                continue;
            }
            // No answer (no network, signed out, the server restarting):
            // again soon, then less often.
            try {
                Thread.sleep(backoff);
            } catch (InterruptedException e) {
                return;
            }
            backoff = Math.min(backoff * 2, 60_000);
        }
    }

    @Override
    public void onDestroy() {
        running = false;
        if (loop != null) loop.interrupt();
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
