// HERTZ RTP output for butt
//
// The normal butt path sends an Ogg Opus stream over Icecast/Shoutcast.
// HERTZ instead accepts raw Opus RTP. This file owns a UDP socket and
// packetizes Opus frames without any Ogg pages or HTTP handshake.

#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <time.h>

#ifdef _WIN32
 #include <winsock2.h>
 #include <ws2tcpip.h>
 #include <windows.h>
 #define hz_close(s) closesocket(s)
 #define hz_send(s, b, n) send((s), (const char*)(b), (n), 0)
#else
 #include <sys/types.h>
 #include <sys/socket.h>
 #include <netinet/in.h>
 #include <arpa/inet.h>
 #include <netdb.h>
 #include <unistd.h>
 #define hz_close(s) close(s)
 #define hz_send(s, b, n) send((s), (b), (n), 0)
#endif

#include "config.h"
#include "cfg.h"
#include "butt.h"
#include "timer.h"
#include "hertz.h"

static int hz_socket = -1;
static unsigned short hz_seq = 0;
static unsigned int hz_timestamp = 0;
static unsigned int hz_ssrc = 0;

int hz_connect(void)
{
    struct hostent *host = NULL;
    struct sockaddr_in dst;
    int sock;

    hz_disconnect();

#ifdef _WIN32
    WSADATA wsa;
    WSAStartup(MAKEWORD(2, 2), &wsa);
#endif

    host = gethostbyname(cfg.srv[cfg.selected_srv]->addr);
    if (host == NULL)
        return 1;

    memset(&dst, 0, sizeof(dst));
    dst.sin_family = AF_INET;
    dst.sin_port = htons((unsigned short)cfg.srv[cfg.selected_srv]->port);
    memcpy(&dst.sin_addr, host->h_addr, host->h_length);

    sock = socket(AF_INET, SOCK_DGRAM, 0);
    if (sock < 0)
        return 2;

    if (connect(sock, (struct sockaddr*)&dst, sizeof(dst)) != 0)
    {
        hz_close(sock);
        return 2;
    }

    hz_socket = sock;
#ifdef _WIN32
    srand((unsigned int)time(NULL) ^ (unsigned int)GetCurrentProcessId());
#else
    srand((unsigned int)time(NULL) ^ (unsigned int)getpid());
#endif
    hz_seq = (unsigned short)(rand() & 0xffff);
    hz_timestamp = (unsigned int)rand();
    hz_ssrc = (unsigned int)rand();

    connected = 1;
    timer_init(&stream_timer, 1);
    return 0;
}

int hz_send_frame(opus_enc *opus, short *pcm_buf, int samples)
{
    unsigned char payload[4096];
    unsigned char packet[12 + sizeof(payload)];
    int payload_len;

    if (hz_socket < 0 || opus == NULL)
        return -1;

    payload_len = opus_enc_encode_frame(opus, pcm_buf, samples, payload, sizeof(payload));
    if (payload_len < 0)
        return -1;
    if (payload_len == 0)
        return 0; // Opus DTX produced no frame; nothing to send.

    packet[0] = 0x80; // RTP version 2, no padding/extension/CSRC
    packet[1] = 111;  // Opus payload type expected by HERTZ
    packet[2] = (unsigned char)(hz_seq >> 8);
    packet[3] = (unsigned char)(hz_seq & 0xff);
    packet[4] = (unsigned char)(hz_timestamp >> 24);
    packet[5] = (unsigned char)(hz_timestamp >> 16);
    packet[6] = (unsigned char)(hz_timestamp >> 8);
    packet[7] = (unsigned char)(hz_timestamp & 0xff);
    packet[8] = (unsigned char)(hz_ssrc >> 24);
    packet[9] = (unsigned char)(hz_ssrc >> 16);
    packet[10] = (unsigned char)(hz_ssrc >> 8);
    packet[11] = (unsigned char)(hz_ssrc & 0xff);
    memcpy(packet + 12, payload, (size_t)payload_len);

    if (hz_send(hz_socket, packet, 12 + payload_len) < 0)
        return -1;

    hz_seq++;
    hz_timestamp += (unsigned int)samples;
    return 0;
}

void hz_disconnect(void)
{
    if (hz_socket >= 0)
    {
        hz_close(hz_socket);
        hz_socket = -1;
    }
}

int hz_is_connected(void)
{
    return hz_socket >= 0;
}
