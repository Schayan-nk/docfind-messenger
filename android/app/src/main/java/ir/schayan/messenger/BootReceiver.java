package ir.schayan.messenger;

import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;

// After the phone restarts, or the app is updated, the connection that brings
// calls and messages is opened again (LiveService).
public class BootReceiver extends BroadcastReceiver {
    @Override
    public void onReceive(Context c, Intent i) {
        LiveService.start(c);
    }
}
