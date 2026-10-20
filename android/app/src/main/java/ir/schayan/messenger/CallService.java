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

// CallService keeps a messenger call going while the app is in the
// background (asked for on 2026-10-14: a call or a shared screen dropped as
// soon as the person left the app). Android takes the microphone and the
// camera away from an app it cannot see unless the app runs a foreground
// service of that type, with a notification saying so; this is that
// service and that notification, for as long as the call lasts. The page
// starts it once the call has its microphone (Bridge.callStart) and stops it
// when the call ends (Bridge.callStop). It records nothing and sends
// nothing: the call itself is still the page's.
public class CallService extends Service {
    static volatile boolean running;

    private static final String CHANNEL = "call";

    static void start(Context c, boolean video) {
        Intent i = new Intent(c, CallService.class).putExtra("video", video);
        try {
            if (Build.VERSION.SDK_INT >= 26) c.startForegroundService(i);
            else c.startService(i);
        } catch (RuntimeException e) {
            // Not allowed from where it was asked (the app was already in the
            // background): the call goes on while the app is on screen.
        }
    }

    static void stop(Context c) {
        c.stopService(new Intent(c, CallService.class));
    }

    @Override
    public int onStartCommand(Intent intent, int flags, int startId) {
        boolean video = intent != null && intent.getBooleanExtra("video", false);
        NotificationManager nm = (NotificationManager) getSystemService(NOTIFICATION_SERVICE);
        if (Build.VERSION.SDK_INT >= 26) {
            nm.createNotificationChannel(new NotificationChannel(CHANNEL, "تماس‌ها", NotificationManager.IMPORTANCE_LOW));
        }
        Intent back = new Intent(this, MainActivity.class).addFlags(Intent.FLAG_ACTIVITY_SINGLE_TOP);
        PendingIntent open = PendingIntent.getActivity(this, 2, back, PendingIntent.FLAG_IMMUTABLE);
        Notification.Builder b = Build.VERSION.SDK_INT >= 26 ? new Notification.Builder(this, CHANNEL)
                : new Notification.Builder(this);
        Notification n = b.setContentTitle(video ? "تماس تصویری در جریان است" : "تماس صوتی در جریان است")
                .setContentText("برای بازگشت به تماس بزنید.")
                .setSmallIcon(android.R.drawable.ic_menu_call)
                .setContentIntent(open)
                .setOngoing(true)
                .build();
        try {
            if (Build.VERSION.SDK_INT >= 30) {
                int type = ServiceInfo.FOREGROUND_SERVICE_TYPE_MICROPHONE;
                if (video) type |= ServiceInfo.FOREGROUND_SERVICE_TYPE_CAMERA;
                startForeground(2, n, type);
            } else {
                startForeground(2, n);
            }
        } catch (RuntimeException e) {
            stopSelf();
            return START_NOT_STICKY;
        }
        running = true;
        return START_NOT_STICKY;
    }

    @Override
    public void onDestroy() {
        running = false;
        super.onDestroy();
    }

    @Override
    public IBinder onBind(Intent intent) {
        return null;
    }
}
