// Headless smoke test for butt's HERTZ RTP sender.
//
// It does not use PortAudio or FLTK. It feeds a 440 Hz stereo tone directly
// into the Opus encoder and the HERTZ RTP packetizer, so it can be run on a
// build machine without a microphone.
//
// Build (MSYS2 UCRT64, from butt-master/tests):
//   g++ -DHAVE_CONFIG_H -I../src -I.. -I/ucrt64/include \
//     hertz_sender_test.cpp ../src/hertz.cpp ../src/opus_encode.cpp ../src/timer.cpp \
//     -L/ucrt64/lib -lopus -logg -lws2_32 -o hertz_sender_test.exe
//
// Run while HERTZ is listening on UDP 5004:
//   PATH=/ucrt64/bin:$PATH ./hertz_sender_test.exe 127.0.0.1 5004

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <math.h>

#ifdef _WIN32
 #include <windows.h>
 #include <mmsystem.h>
 #define sleep_ms(ms) Sleep(ms)
#else
 #include <unistd.h>
 #define sleep_ms(ms) usleep((ms) * 1000)
#endif

#include "config.h"
#include "cfg.h"
#include "butt.h"
#include "timer.h"
#include "hertz.h"

// hertz.cpp/timer.cpp expect these globals from butt.
config_t cfg;
bool connected;
sec_timer stream_timer;

int main(int argc, char **argv)
{
    const char *host = (argc > 1) ? argv[1] : "127.0.0.1";
    int port = (argc > 2) ? atoi(argv[2]) : 5004;
    opus_enc opus;
    short pcm[960 * 2];
    int frame, i;

    memset(&cfg, 0, sizeof(cfg));
    cfg.selected_srv = 0;
    cfg.srv = (server_t**)malloc(sizeof(server_t*));
    cfg.srv[0] = (server_t*)calloc(1, sizeof(server_t));
    cfg.srv[0]->addr = (char*)malloc(strlen(host) + 1);
    strcpy(cfg.srv[0]->addr, host);
    cfg.srv[0]->port = port;
    cfg.srv[0]->type = HERTZ;

    memset(&opus, 0, sizeof(opus));
    opus.channel = 2;
    opus.samplerate = 48000;
    opus.bitrate = 128000;
    if (opus_enc_init(&opus) != 0)
    {
        fprintf(stderr, "opus init failed\n");
        return 1;
    }

    if (hz_connect() != 0)
    {
        fprintf(stderr, "HERTZ UDP connect failed\n");
        return 1;
    }

#ifdef _WIN32
    timeBeginPeriod(1); // make Sleep(20) actually ~20 ms
#endif
    fprintf(stderr, "sending 5 s of 440 Hz stereo Opus RTP to %s:%d\n", host, port);
    for (frame = 0; frame < 250; frame++)
    {
        for (i = 0; i < 960; i++)
        {
            double t = (frame * 960 + i) / 48000.0;
            short sample = (short)(12000.0 * sin(2.0 * 3.141592653589793 * 440.0 * t));
            pcm[i * 2] = sample;
            pcm[i * 2 + 1] = sample;
        }

        if (hz_send_frame(&opus, pcm, 960) != 0)
        {
            fprintf(stderr, "send failed at frame %d\n", frame);
            hz_disconnect();
            return 1;
        }
        sleep_ms(20);
    }

    hz_disconnect();
    opus_enc_close(&opus);
#ifdef _WIN32
    timeEndPeriod(1);
#endif
    fprintf(stderr, "done\n");
    return 0;
}
