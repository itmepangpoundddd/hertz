// FLTK GUI related functions
//
// Copyright 2007-2008 by Daniel Noethen.
//
// This program is free software; you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation; either version 2, or (at your option)
// any later version.
//
// This program is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE.  See the
// GNU General Public License for more details.
//
#include <stdlib.h>
#include <string.h>
#include <errno.h>
#include <unistd.h>
#include <signal.h>
#include <time.h>
#include <sys/stat.h>
#include <FL/fl_draw.H>

#ifndef _WIN32
 #include <sys/wait.h>
#endif

#include "config.h"

#include "cfg.h"
#include "butt.h"
#include "util.h"
#include "port_audio.h"
#include "timer.h"
#include "flgui.h"
#include "fl_funcs.h"
#include "shoutcast.h"
#include "icecast.h"
#include "strfuncs.h"
#include "hertz_theme.h"


/* HERTZ palette: a dark slate UI with the same cyan accent used by the HERTZ
 * admin dashboard. Kept local to the GUI layer so the encoder and ingest code
 * stay unchanged. */
static bool hz_is_window(Fl_Widget *w)
{
    return w == fl_g->window_main || w == fl_g->window_cfg ||
           w == fl_g->window_add_srv || w == fl_g->window_add_icy;
}

static void hz_theme_widget(Fl_Widget *w)
{
    if(w == NULL)
        return;

    if(Fl_Tabs *tabs = dynamic_cast<Fl_Tabs*>(w))
    {
        tabs->color(HZ_PANEL);
        tabs->selection_color(HZ_ACCENT);
        tabs->labelcolor(HZ_TEXT);
        tabs->labelfont(FL_HELVETICA_BOLD);
        tabs->labelsize(11);
    }

    if(Fl_Text_Display *text = dynamic_cast<Fl_Text_Display*>(w))
    {
        text->color(HZ_INPUT);
        text->textcolor(HZ_TEXT);
        text->selection_color(HZ_ACCENT);
        text->textfont(FL_COURIER);
        text->textsize(12);
    }
    else if(Fl_Choice *choice = dynamic_cast<Fl_Choice*>(w))
    {
        choice->color(HZ_INPUT);
        choice->textcolor(HZ_TEXT);
        choice->selection_color(HZ_ACCENT);
        choice->labelfont(FL_HELVETICA);
        choice->labelsize(11);
        choice->textsize(11);
    }
    else if(Fl_Value_Input *value_input = dynamic_cast<Fl_Value_Input*>(w))
    {
        value_input->color(HZ_INPUT);
        value_input->textcolor(HZ_TEXT);
        value_input->selection_color(HZ_ACCENT);
        value_input->labelfont(FL_HELVETICA);
        value_input->labelsize(11);
        value_input->textsize(11);
    }
    else if(Fl_Input_ *input = dynamic_cast<Fl_Input_*>(w))
    {
        input->color(HZ_INPUT);
        input->textcolor(HZ_TEXT);
        input->selection_color(HZ_ACCENT);
        input->labelfont(FL_HELVETICA);
        input->labelsize(11);
        input->textsize(11);
    }
    else if(Fl_Check_Button *check = dynamic_cast<Fl_Check_Button*>(w))
    {
        check->color(HZ_BG);
        check->selection_color(HZ_ACCENT);
        check->labelcolor(HZ_TEXT);
        check->labelfont(FL_HELVETICA);
        check->labelsize(11);
    }
    else if(Fl_Round_Button *radio = dynamic_cast<Fl_Round_Button*>(w))
    {
        radio->color(HZ_BG);
        radio->selection_color(HZ_ACCENT);
        radio->labelcolor(HZ_TEXT);
        radio->labelfont(FL_HELVETICA);
        radio->labelsize(11);
    }
    else if(Fl_Button *button = dynamic_cast<Fl_Button*>(w))
    {
        const char *label = button->label();
        bool modern_button = label != NULL &&
            (strcmp(label, "Connect") == 0 || strcmp(label, "Disconnect") == 0 ||
             strcmp(label, "Record") == 0 || strcmp(label, "Settings") == 0 ||
             strcmp(label, "Close settings") == 0 || strcmp(label, "Hide log") == 0 ||
             strcmp(label, "Show log") == 0);

        if(!modern_button)
        {
            button->box(FL_ROUND_UP_BOX);
            button->down_box(FL_ROUND_DOWN_BOX);
            button->color(HZ_BUTTON);
            button->selection_color(HZ_ACCENT);
            button->labelcolor(HZ_TEXT);
            button->labelfont(FL_HELVETICA_BOLD);
            button->labelsize(11);
        }
    }
    else if(Fl_Box *box = dynamic_cast<Fl_Box*>(w))
    {
        bool modern_box = box == fl_g->brand_title || box == fl_g->brand_sub ||
            box == fl_g->status_pill || box == fl_g->status_text ||
            box == fl_g->status_detail || box == fl_g->server_label ||
            box == fl_g->codec_label || box == fl_g->bitrate_label ||
            box == fl_g->stream_stats || box == fl_g->VU_Text;
        if(!modern_box)
        {
            box->labelcolor(HZ_MUTED);
            box->labelfont(FL_HELVETICA);
            box->labelsize(11);
        }
    }

    if(Fl_Group *group = dynamic_cast<Fl_Group*>(w))
    {
        if(!hz_is_window(w) && dynamic_cast<Fl_Tabs*>(w) == NULL)
        {
            Fl_Boxtype current = group->box();
            if(current == FL_ENGRAVED_FRAME || current == FL_ENGRAVED_BOX ||
               current == FL_DOWN_FRAME || current == FL_DOWN_BOX)
            {
                group->box(FL_ROUNDED_BOX);
                group->color(HZ_PANEL);
            }
            else if(current == FL_ROUNDED_BOX || current == FL_ROUNDED_FRAME)
            {
                group->color(HZ_PANEL);
            }
            else
            {
                group->color(HZ_BG);
            }
            group->labelcolor(HZ_MUTED);
            group->labelfont(FL_HELVETICA_BOLD);
            group->labelsize(11);
        }
        for(int i = 0; i < group->children(); i++)
            hz_theme_widget(group->child(i));
    }
}

static void hz_theme_window(Fl_Window *win)
{
    if(win == NULL)
        return;
    win->color(HZ_BG);
    win->selection_color(HZ_BG);
    win->labelcolor(HZ_TEXT);
    for(int i = 0; i < win->children(); i++)
        hz_theme_widget(win->child(i));
    win->redraw();
}

void hertz_apply_theme(void)
{
    Fl::background(11, 18, 32);
    Fl::background2(17, 28, 46);
    Fl::foreground(232, 241, 250);
    Fl::set_font(FL_HELVETICA, "Segoe UI");
    Fl::set_font(FL_HELVETICA_BOLD, "Segoe UI Semibold");
    Fl::set_font(FL_COURIER, "Cascadia Mono");

    /* Migrate the old butt defaults only; custom LCD colors are preserved. */
    if(cfg.main.bg_color == 151540480)
        cfg.main.bg_color = (int)HZ_BG;
    if(cfg.main.txt_color == -256)
        cfg.main.txt_color = (int)HZ_TEXT;

    fl_g->window_main->label(HERTZ_BUTT_NAME " " PACKAGE_VERSION);
    fl_g->window_cfg->label(HERTZ_BUTT_NAME " settings");

    hz_theme_window(fl_g->window_main);
    hz_theme_window(fl_g->window_cfg);
    hz_theme_window(fl_g->window_add_srv);
    hz_theme_window(fl_g->window_add_icy);

    fl_g->button_gui_bg_color->color(cfg.main.bg_color,
            fl_lighter((Fl_Color)cfg.main.bg_color));
    fl_g->button_gui_text_color->color(cfg.main.txt_color,
            fl_lighter((Fl_Color)cfg.main.txt_color));
    fl_g->window_main->redraw();
    fl_g->window_cfg->redraw();
    hertz_update_status();
}


void hertz_update_status(void)
{
    if(fl_g == NULL || fl_g->status_text == NULL)
        return;

    const char *state = "IDLE";
    const char *detail = "Ready to broadcast";
    Fl_Color color = HZ_MUTED;

    if(recording)
    {
        state = "RECORDING";
        detail = "Writing the broadcast to disk";
        color = HZ_BAD;
    }
    else if(connected && streaming)
    {
        state = "ON AIR";
        detail = "Live audio is being sent";
        color = HZ_GOOD;
    }
    else if(try_connect)
    {
        state = "CONNECTING";
        detail = "Waiting for the server";
        color = HZ_WARN;
    }
    else if(connected)
    {
        state = "CONNECTED";
        detail = "Connected, not streaming";
        color = HZ_GOOD;
    }

    fl_g->status_text->label(state);
    fl_g->status_text->labelcolor(color);
    fl_g->status_pill->label(state);
    fl_g->status_pill->labelcolor(color);
    fl_g->status_detail->label(detail);

    if(cfg.main.num_of_srv > 0 && cfg.srv != NULL && cfg.selected_srv >= 0 &&
       cfg.selected_srv < cfg.main.num_of_srv && cfg.srv[cfg.selected_srv] != NULL)
        fl_g->server_label->copy_label(cfg.srv[cfg.selected_srv]->name);
    else
        fl_g->server_label->label("No server selected");

    const char *codec = cfg.audio.codec != NULL ? cfg.audio.codec : "—";
    char buf[128];
    snprintf(buf, sizeof(buf), "%s · %d Hz · %s", codec,
             cfg.audio.samplerate, cfg.audio.channel == 1 ? "Mono" : "Stereo");
    fl_g->codec_label->copy_label(buf);

    snprintf(buf, sizeof(buf), "%d kbps", cfg.audio.bitrate);
    fl_g->bitrate_label->copy_label(buf);

    fl_g->window_main->redraw();
}

void fill_cfg_widgets(void)
{
    int i;

    int bitrate[] = { 8, 16, 24, 32, 40, 48, 56, 64, 80, 96, 112,
                    128, 160, 192, 224, 256, 320, 0 };

  
    //fill the main section
    for(i = 0; i < cfg.audio.dev_count; i++)
        fl_g->choice_cfg_dev->add(cfg.audio.pcm_list[i]->name);

    fl_g->choice_cfg_dev->value(cfg.audio.dev_num);
    fl_g->choice_cfg_source_channel->value(cfg.audio.channels / 2 - 1);

    fl_g->choice_cfg_act_srv->clear();
    fl_g->choice_cfg_act_srv->redraw();
    for(i = 0; i < cfg.main.num_of_srv; i++)
        fl_g->choice_cfg_act_srv->add(cfg.srv[i]->name);

    if(cfg.main.num_of_srv > 0)
    {
        fl_g->button_cfg_edit_srv->activate();
        fl_g->button_cfg_del_srv->activate();
    }
    else
    {
        fl_g->button_cfg_edit_srv->deactivate();
        fl_g->button_cfg_del_srv->deactivate();
    }

    fl_g->choice_cfg_act_icy->clear();
    fl_g->choice_cfg_act_icy->redraw();
    for(i = 0; i < cfg.main.num_of_icy; i++)
        fl_g->choice_cfg_act_icy->add(cfg.icy[i]->name);

    if(cfg.main.num_of_icy > 0)
    {
        fl_g->button_cfg_edit_icy->activate();
        fl_g->button_cfg_del_icy->activate();
    }
    else
    {
        fl_g->button_cfg_edit_icy->deactivate();
        fl_g->button_cfg_del_icy->deactivate();
    }

    fl_g->choice_cfg_act_srv->value(cfg.selected_srv);
    fl_g->choice_cfg_act_icy->value(cfg.selected_icy);


    fl_g->check_cfg_connect->value(cfg.main.connect_at_startup);
    fl_g->input_log_filename->value(cfg.main.log_file);

    //fill the audio (stream) section
    if(!strcmp(cfg.audio.codec, "mp3"))
        fl_g->choice_cfg_codec->value(CHOICE_MP3);
    else if(!strcmp(cfg.audio.codec, "ogg"))
        fl_g->choice_cfg_codec->value(CHOICE_OGG);
    else if(!strcmp(cfg.audio.codec, "opus"))
        fl_g->choice_cfg_codec->value(CHOICE_OPUS);
    else if(!strcmp(cfg.audio.codec, "aac"))
        fl_g->choice_cfg_codec->value(CHOICE_AAC);


    if(cfg.audio.channel == 1)
        fl_g->choice_cfg_channel->value(CHOICE_MONO);
    else
        fl_g->choice_cfg_channel->value(CHOICE_STEREO);

    for(i = 0; bitrate[i] != 0; i++)
        if(cfg.audio.bitrate == bitrate[i])
        {
            fl_g->choice_cfg_bitrate->value(i);
            break;
        }

    fl_g->input_cfg_buffer->value(cfg.audio.buffer_ms);

    fl_g->choice_cfg_resample_mode->value(cfg.audio.resample_mode);
    

    if(cfg.main.song_update)
        fl_g->check_song_update_active->value(1);
    else
        fl_g->check_song_update_active->value(0);

    fl_g->input_cfg_song_file->value(cfg.main.song_path);

    //fill the record section
    fl_g->input_rec_filename->value(cfg.rec.filename);
    fl_g->input_rec_folder->value(cfg.rec.folder);
    fl_g->input_rec_split_time->value(cfg.rec.split_time);

    if(!strcmp(cfg.rec.codec, "mp3"))
        fl_g->choice_rec_codec->value(CHOICE_MP3);
    else if(!strcmp(cfg.rec.codec, "ogg"))
        fl_g->choice_rec_codec->value(CHOICE_OGG);
    else if(!strcmp(cfg.rec.codec, "opus"))
        fl_g->choice_rec_codec->value(CHOICE_OPUS);
    else if(!strcmp(cfg.rec.codec, "aac"))
        fl_g->choice_rec_codec->value(CHOICE_AAC);
    else if(!strcmp(cfg.rec.codec, "flac"))
    {
        fl_g->choice_rec_codec->value(CHOICE_FLAC);
        fl_g->choice_rec_bitrate->deactivate();
    }
    else //wav
    {
        fl_g->choice_rec_codec->value(CHOICE_WAV);
        fl_g->choice_rec_bitrate->deactivate();
    }

    for(i = 0; bitrate[i] != 0; i++)
        if(cfg.rec.bitrate == bitrate[i])
            fl_g->choice_rec_bitrate->value(i);

    if(cfg.rec.start_rec)
        fl_g->check_cfg_rec->value(1);
    else
        fl_g->check_cfg_rec->value(0);

    if(cfg.rec.sync_to_hour)
        fl_g->check_sync_to_full_hour->value(1);
    else
        fl_g->check_sync_to_full_hour->value(0);


    update_samplerates();

    //fill the GUI section
    fl_g->button_gui_bg_color->color(cfg.main.bg_color,
            fl_lighter((Fl_Color)cfg.main.bg_color));
    fl_g->button_gui_text_color->color(cfg.main.txt_color,
            fl_lighter((Fl_Color)cfg.main.txt_color));
    fl_g->check_gui_attach->value(cfg.gui.attach);
    fl_g->check_gui_ontop->value(cfg.gui.ontop);
    if(cfg.gui.ontop)
    {
        fl_g->window_main->stay_on_top(1);
        fl_g->window_cfg->stay_on_top(1);
    }
    fl_g->check_gui_lcd_auto->value(cfg.gui.lcd_auto);
    hertz_update_status();
}

//Updates the samplerate drop down menu for the audio
//device the user has selected
// this does not only update the sample rates but also the input channels
void update_samplerates(void)
{
    int i;
    int *sr_list;
    char sr_asc[10];

    fl_g->choice_cfg_samplerate->clear();

    sr_list = cfg.audio.pcm_list[cfg.audio.dev_num]->sr_list;

    for(i = 0; sr_list[i] != 0; i++)
    {
        snprintf(sr_asc, 10, "%dHz", sr_list[i]);
        fl_g->choice_cfg_samplerate->add(sr_asc);

        if(cfg.audio.samplerate == sr_list[i])
            fl_g->choice_cfg_samplerate->value(i);
    }
    if(i == 0)
    {
        fl_g->choice_cfg_samplerate->add("dev. not supported");
        fl_g->choice_cfg_samplerate->value(0);
    }

    fl_g->choice_cfg_source_channel->clear();
    for(i = 0; i < cfg.audio.pcm_list[cfg.audio.dev_num]->num_of_channels; i += 2)
    {
        // we list channels as starting with 1, in stereo pairs only
        snprintf(sr_asc, 10, "%d \\/ %d", i + 1, i + 2);
        fl_g->choice_cfg_source_channel->add(sr_asc);
        // cfg.audio.channels is the number of channels we have to read
        // but the value of the choice box is 0-based consecutive
        if (cfg.audio.channels == (i + 2))
            fl_g->choice_cfg_source_channel->value(i/2);
    }
}

void print_info(const char* info, int info_type)
{
    char timebuf[10];
    char logtimestamp[21];
    char* infotxt;
    FILE *log_fd;
    int len;

    time_t test;
    struct tm  *mytime;
    static struct tm time_bak;

    infotxt = strdup(info);


    test = time(NULL);
    mytime = localtime(&test);

    if( (time_bak.tm_min != mytime->tm_min) || (time_bak.tm_hour != mytime->tm_hour) )
    {
        time_bak.tm_min = mytime->tm_min;
        time_bak.tm_hour = mytime->tm_hour;
        strftime(timebuf, sizeof(timebuf), "\n%H:%M:", mytime);
        fl_g->info_buffer->append(timebuf);
    }

    fl_g->info_buffer->append((const char*)"\n");
    fl_g->info_buffer->append((const char*)info);

    //always scroll to the last line
    fl_g->info_output->scroll(fl_g->info_buffer->count_lines(0,     //count the lines from char 0 to the last character
                            fl_g->info_buffer->length()),           //returns the number of characters in the buffer
                            0);

    // log to log_file if defined
    if ((cfg.main.log_file != NULL) && (strlen(cfg.main.log_file) > 0))
    {
        strftime(logtimestamp, sizeof(logtimestamp), "%Y-%m-%d %H:%M:%S", mytime);
        log_fd = fopen(cfg.main.log_file, "ab");
        if (log_fd != NULL) 
        {
            // Probably the most confusing part of the whole code ;)
            
            if(strchr(infotxt, ':'))
                strrpl(&infotxt, (char*)"\n", (char*)", ", MODE_ALL);
            else
                strrpl(&infotxt, (char*)"\n", (char*)" ", MODE_ALL);

            strrpl(&infotxt, (char*)":,", (char*)": ", MODE_ALL);
            strrpl(&infotxt, (char*)"\t", (char*)"", MODE_ALL);

            len = strlen(infotxt)-1;
            // remove trailing commas and spaces 
            while (infotxt[len] == ',' || infotxt[len] == ' ')
            {
                infotxt[len--] = '\0';
            }

            fprintf(log_fd, "%s %s\n", logtimestamp, infotxt);
            fclose(log_fd);
        }

    }
    
    free(infotxt);
}

void print_lcd(const char *text, int len, int home, int clear)
{
    if(clear)
        fl_g->lcd->clear();

    fl_g->lcd->print((const uchar*)text, len);

    if(home)
        fl_g->lcd->cursor_pos(0);

    if(fl_g->stream_stats != NULL && strchr(text, '\n') != NULL)
        fl_g->stream_stats->copy_label(text);

    hertz_update_status();
}


void expand_string(char **str)
{
    char m[3], d[3], y[5], hh[3], mi[3], ss[3];
    struct tm *date;
    const time_t t = time(NULL);

    date = localtime(&t);

    snprintf(ss,  3, "%02d", date->tm_sec);
    snprintf(mi,  3, "%02d", date->tm_min);
    snprintf(hh,  3, "%02d", date->tm_hour);
    snprintf(d,   3, "%02d", date->tm_mday);
    snprintf(m,   3, "%02d", date->tm_mon+1);
    snprintf(y,   5, "%d",   date->tm_year+1900);
    
    strrpl(str, (char*)"%S", ss, MODE_ALL);
    strrpl(str, (char*)"%M", mi, MODE_ALL);
    strrpl(str, (char*)"%H", hh, MODE_ALL);
    strrpl(str, (char*)"%d", d, MODE_ALL);
    strrpl(str, (char*)"%m", m, MODE_ALL);
    strrpl(str, (char*)"%y", y, MODE_ALL);
    strrpl(str, (char*)"%Y", y, MODE_ALL); // %Y and %y both work as year
}

void test_file_extension(void)
{
    char *current_ext;

    current_ext = util_get_file_extension(cfg.rec.filename);

    // Append extension
    if(current_ext == NULL)
    {
        cfg.rec.filename = (char*)realloc(cfg.rec.filename, strlen(cfg.rec.filename)+strlen(cfg.rec.codec)+2);
        strcat(cfg.rec.filename, ".");
        strcat(cfg.rec.filename, cfg.rec.codec);
        fl_g->input_rec_filename->value(cfg.rec.filename);
    }
    // Replace extension
    else if(strcmp(current_ext, cfg.rec.codec))
    {
        strrpl(&cfg.rec.filename, current_ext, cfg.rec.codec, MODE_LAST);
        fl_g->input_rec_filename->value(cfg.rec.filename);
    }
}



void init_main_gui_and_audio(void)
{
    fl_g->slider_gain->value(util_factor_to_db(cfg.main.gain));
    fl_g->window_main->redraw();

    if(cfg.gui.ontop)
        fl_g->window_main->stay_on_top(1);

    lame_stream.channel = cfg.audio.channel;
    lame_stream.bitrate = cfg.audio.bitrate;
    lame_stream.samplerate = cfg.audio.samplerate;
    lame_enc_reinit(&lame_stream);

    lame_rec.channel = cfg.audio.channel;
    lame_rec.bitrate = cfg.rec.bitrate;
    lame_rec.samplerate = cfg.audio.samplerate;
    lame_enc_reinit(&lame_rec);

    vorbis_stream.channel = cfg.audio.channel;
    vorbis_stream.bitrate = cfg.audio.bitrate;
    vorbis_stream.samplerate = cfg.audio.samplerate;
    vorbis_enc_reinit(&vorbis_stream);

    vorbis_rec.channel = cfg.audio.channel;
    vorbis_rec.bitrate = cfg.rec.bitrate;
    vorbis_rec.samplerate = cfg.audio.samplerate;
    vorbis_enc_reinit(&vorbis_rec);
    
    opus_stream.channel = cfg.audio.channel;
    opus_stream.bitrate = cfg.audio.bitrate*1000;
    opus_stream.samplerate = cfg.audio.samplerate;
    opus_enc_reinit(&opus_stream);
    
    opus_rec.channel = cfg.audio.channel;
    opus_rec.bitrate = cfg.rec.bitrate*1000;
    opus_rec.samplerate = cfg.audio.samplerate;
    opus_enc_reinit(&opus_rec);

    aac_stream.channel = cfg.audio.channel;
    aac_stream.bitrate = cfg.audio.bitrate;
    aac_stream.samplerate = cfg.audio.samplerate;
    aac_stream.aot = cfg.audio.aac_aot; 
    aac_enc_reinit(&aac_stream);

    aac_rec.channel = cfg.audio.channel;
    aac_rec.bitrate = cfg.rec.bitrate;
    aac_rec.samplerate = cfg.audio.samplerate;
    aac_rec.aot = cfg.audio.aac_aot; 
    aac_enc_reinit(&aac_rec);

    flac_rec.channel = cfg.audio.channel;
    flac_rec.samplerate = cfg.audio.samplerate;
    flac_enc_reinit(&flac_rec);
}

