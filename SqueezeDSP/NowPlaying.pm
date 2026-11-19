package Plugins::SqueezeDSP::NowPlaying;
# What our streams send the player, for now playing on LMS 9.2.
#
# LMS 9.2's status query says when a stream is transcoded (is_transcoded) and gives the stream's own type, bitrate,
# samplerate and samplesize beside the track's (Queries.pm:4124-4160); Material shows the stream's from 9.2 (server.js,
# LMS_VERSION 90200). For a transcode LMS can only guess them: the rate a D rule took the track down to, otherwise 44.1k
# (48k for a track above 44.1k), and the track's own sample size (Song.pm:629-656). So every stream through our engine
# would show 44.1k or 48k and the source's depth, whatever it sends: 352.8k after Upsample, DSD256 out of the converter.
# LMS asks a plugin first: Slim::Player::Song::handleSampleAndBitrate($song, $transcoder), once a transcode's pipeline
# is open, with the transcoder its command was built from (Song.pm:636-650, LMS 9.2.0); a hash of bitrate, sampleSize
# and sampleRate, or false for LMS's own guess. Earlier LMS never calls it. The bitrate LMS then guesses from those
# (guessBitrateFromFormat) also sizes its buffer and rebuffering thresholds, which 9.1 based on 44.1k/16.
#
# The wrap of tokenizeConvertCommand2 (Upsample.pm) hands each of our commands here as LMS built it (streamStart), and
# the hook reads what it sends as the page reads a running pipeline (Status::streamOutput): FLAC or WAV at its rate and
# depth, a DSF at its DSD rate, 1-bit (as LMS records a DSF track). A reduced stream (MP3, Opus, Ogg) keeps LMS's own
# handling, as does every stream not ours; the hook that was there before ours still gets those.
#
# LMS has ONE such method, so a plugin setting its own later replaces ours: DSDPlayer does on 9.2, from its
# postinitPlugin (after every plugin's initPlugin), for its [dsdplay] rules, handing nothing on (Plugins::DSDPlayer::Song,
# LMS-Community/plugin-DSDPlayer 9eeb22f): on plex every stream of ours then showed LMS's guess. So ours goes back on top
# at each of our streams' start, just before LMS asks (streamStart), keeping the one it found as the one before it.
use strict;

# where streamStart leaves the command on LMS's transcoder
use constant KEY => 'sqdsp_command';

my $prev;
# set while a hook we handed on to runs: one that took ours as the one before it hands back here, and gets nothing
our $BUSY;

# Ours on LMS's Song, the hook there (if any) kept as the one before it; nothing when ours is there already
sub install {
    my $cur = Slim::Player::Song->can('handleSampleAndBitrate');
    return 1 if $cur && $cur == \&_hook;
    $prev = $cur;
    no strict 'refs';
    no warnings 'redefine';
    *{'Slim::Player::Song::handleSampleAndBitrate'} = \&_hook;
    return 1;
}

# At each of our streams' start (the wrap, Upsample.pm): the command as LMS will run it, kept on its transcoder, which
# Song::open hands to the hook once the pipeline is open, and ours put back on top if another took its place. Our rules
# only (their [SqueezeDSP] tag, Gapless::ours).
sub streamStart {
    my ( $t, $command ) = @_;
    return unless defined $command && Plugins::SqueezeDSP::Gapless::ours($t);
    $t->{ +KEY } = $command;
    install();
}

sub _hook {
    my ( $song, $t ) = @_;
    return undef if $BUSY;
    my $r = eval { result($t) };
    return $r if $r;
    return undef unless $prev;
    local $BUSY = 1;
    return $prev->(@_);
}

# What one of our streams sends the player, as LMS takes it: { bitrate, sampleSize, sampleRate }, or undef (not ours, a
# reduced stream, or the command not read)
sub result {
    my $t = shift;
    return undef unless ref $t eq 'HASH' && defined $t->{ +KEY };
    my $client = Slim::Player::Client::getClient( $t->{clientid} );
    my $o = Plugins::SqueezeDSP::Status::streamOutput( $t->{ +KEY }, $t->{clientid}, $t->{sampleRate}, $client )
        or return undef;
    return undef if $o->{reduced};
    if ( ( $o->{format} // '' ) eq 'DSF' ) {
        return undef unless ( $o->{dsd} // '' ) =~ /^\d+$/ && $o->{dsd} > 0;
        return { bitrate => undef, sampleSize => 1, sampleRate => 0 + $o->{dsd} };
    }
    return undef unless ( $o->{rate} // '' ) =~ /^\d+$/ && $o->{rate} > 0;
    my $bits = ( $o->{bits} // '' ) =~ /^\d+$/ ? 0 + $o->{bits} : 24;
    return { bitrate => undef, sampleSize => $bits, sampleRate => 0 + $o->{rate} };
}

1;
