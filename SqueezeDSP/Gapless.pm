package Plugins::SqueezeDSP::Gapless;
# The gapless round (2026-11-05, ledger THE GAPLESS ROUND): every stage of the engine with a memory runs across a gapless
# join as one stream, and a track that nothing follows plays its filters out (Simon: "We cant have gaps where filters
# are not applied. It works for HQPlayer gapless and Roon so this needs to."). LMS runs an engine a track, and an engine
# cannot see what follows it, so this module tells it, in a note per player in the engine's temp folder (gapless_<id>,
# the id cleaned as the engine cleans it, sanitizeClientID), written whole and renamed into place (engine gapless.go):
#   continues=1   LMS streams this track gaplessly after the last one: its queue holds both (StreamingController.pm
#                 _Stream puts the new song on before Song::open builds the command; two at most, Bug 5103), the last
#                 still streaming or playing, and this one is no seek (Song::open sets seekdata, and a seek through the
#                 rule $transcoder->{start}, before it builds the command);
#   prev=<url>    the last song's stream URL, which LMS gave that song's engine as --trackURL ($URL$ is the song's
#                 streamUrl, Song.pm:577): the engine takes a residual only from that track;
#   this=, next=  this track's and the next one's rate, kind (pcm or dsd) and channels, as LMS knows them;
#   follows=1     a track follows this one (as the controller's nextsong will pick it: repeat, shuffle, the end);
#   exact=1       the player needs a stream's true length before its first byte (the exact-length round, step 4,
#                 docs/exact-length-plan.md part B): our per-player option says so (exactLength); the engine then
#                 decides the stream's end at its start, from this note, and its header states that stream's count.
# The whole note at each of our streams' start (the wrap of tokenizeConvertCommand2, Upsample.pm); at every change to a
# player's playlist its follows and next again, as the engine reads the note once more when it reaches its input's end,
# a few seconds before the track stops playing (the player's buffer). A queue changed inside those seconds can still
# cost that one join: a track added then follows after the last one's tails played out (a short gap), one removed takes
# the tail the last one kept for it. Under the exact contract the end was decided at the start, so a next track removed
# (or swapped for another output format) at any time during the track leaves the held tail in the residual, and it is
# not played: nothing is ever added to a play queue (Simon 2026-11-15). tail() builds the command that would play it on
# its own (the engine's --tail); nothing queues it.
use strict;
use File::Spec;

my @FIELDS = qw(continues prev this follows next exact);

# each player's last stream of ours, the command as LMS will run it (tail)
my %LAST;

# A player that needs a stream's true length before its first byte (a UPnP or bridged player that gets stuck at the end
# of a track) has our own per-player option on: "Exact track length" (ExactLength 1 in its settings), off by default,
# so every other player plays as before. Ours alone: no other plugin has to declare anything (Simon 2026-11-15).
sub exactLength {
    my $client = shift;
    return 0 unless ref $client;
    my $v = eval { Plugins::SqueezeDSP::Configuration::clientPrefs($client)->{ExactLength} };
    return ( defined $v && "$v" eq '1' ) ? 1 : 0;
}

# The note's path for a player id; undef without the temp folder or an id
sub path {
    my $id  = shift;
    my $dir = $Plugins::SqueezeDSP::Plugin::pluginTempDataDir;
    return undef unless defined $dir && length $dir && defined $id && length $id;
    ( my $clean = $id ) =~ tr/:-/__/;
    return File::Spec->catfile( $dir, "gapless_$clean" );
}

# One of our rules: it runs our engine (its [SqueezeDSP] tag, as WavLength::ours and Configuration know our rules), in
# any format
sub ours {
    my $t = shift;
    return 0 unless ref $t eq 'HASH';
    my $tag = '[' . ( $Plugins::SqueezeDSP::Plugin::convolver || 'SqueezeDSP' ) . ']';
    return index( $t->{command} // '', $tag ) >= 0 ? 1 : 0;
}

# A track's rate, kind and channels for the note, "44100:pcm:2"; a part LMS does not know is 0 or empty
sub kind {
    my $track = shift;
    return '' unless ref $track;
    my $ct   = eval { $track->content_type } // '';
    my $rate = eval { $track->samplerate } // 0;
    my $ch   = eval { $track->channels } // 0;
    $rate = 0 unless $rate =~ /^\d+$/;
    $ch   = 0 unless $ch   =~ /^\d+$/;
    my $k = !length $ct ? '' : $ct =~ /^(?:dsf|dff)$/i ? 'dsd' : 'pcm';
    return "$rate:$k:$ch";
}

# Whether a song follows the streaming one, and its kind: the song the controller's nextsong will pick
# (StreamingController.pm:848-896: repeat one, the same; repeat all, back to the first; else none past the end; the
# playlist's own order, shuffled or not, Playlist::song), worked out here because nextsong is no query: at the end with
# shuffle and repeat all it reshuffles the playlist (reshuffleOnRepeat), which a note must never do. There the next song
# is not known until LMS picks it: one follows, its kind unknown.
sub following {
    my ( $client, $c ) = @_;
    my $count = eval { Slim::Player::Playlist::count($client) } || 0;
    my $song  = eval { $c->streamingSong };
    my $cur   = $song ? eval { $song->index } : undef;
    return ( 0, '' ) unless $count && defined $cur;
    my $repeat = eval { Slim::Player::Playlist::repeat($client) } || 0;
    my $next = $repeat == 1 ? $cur : $cur + 1;
    if ( $next >= $count ) {
        return ( 0, '' ) unless $repeat;
        return ( 1, '' ) if eval { Slim::Player::Playlist::shuffle($client) };
        $next = 0;
    }
    my $track = eval { Slim::Player::Playlist::song( $client, $next ) };
    return ( 1, kind($track) );
}

# The note as the octets LMS gave the engine: a URL as a character string goes out as its UTF-8, as the command does
# (tokenizeConvertCommand2 builds it under use bytes)
sub _octets {
    my $v = shift // '';
    $v =~ s/[\r\n]//g;
    utf8::encode($v) if utf8::is_utf8($v);
    return $v;
}

sub write_note {
    my ( $id, $n ) = @_;
    my $p = path($id) or return 0;
    my $text = join '', map { "$_=" . _octets( $n->{$_} ) . "\n" } @FIELDS;
    my $tmp = "$p.$$";
    open( my $fh, '>', $tmp ) or return 0;
    binmode $fh;
    my $ok = print {$fh} $text;
    $ok = close($fh) && $ok;
    unless ( $ok && rename( $tmp, $p ) ) {
        unlink $tmp;
        return 0;
    }
    return 1;
}

sub read_note {
    my $id = shift;
    my $p = path($id) or return undef;
    open( my $fh, '<', $p ) or return undef;
    binmode $fh;
    my %n;
    while ( my $line = <$fh> ) {
        chomp $line;
        my ( $k, $v ) = split /=/, $line, 2;
        $n{$k} = $v if defined $v;
    }
    close $fh;
    return \%n;
}

# At the start of one of our streams (Upsample.pm's wrap, after LMS built the command): the whole note for its player
sub streamStart {
    my ( $t, $command ) = @_;
    unless ( ours($t) ) {
        delete $LAST{ $t->{clientid} } if ref $t eq 'HASH' && defined $t->{clientid};
        return 0;
    }
    $LAST{ $t->{clientid} } = $command if defined $command && defined $t->{clientid};
    my $client = Slim::Player::Client::getClient( $t->{clientid} ) or return 0;
    my $c = $client->controller or return 0;
    my $q = eval { $c->songqueue } || [];
    my $song = $q->[0] or return 0;
    my ( $continues, $prev ) = ( 0, '' );
    if ( @$q == 2 && !$song->seekdata && !$t->{start} ) {
        my $st = eval { $q->[1]->status } // -1;
        if ( $st == Slim::Player::Song::STATUS_STREAMING() || $st == Slim::Player::Song::STATUS_PLAYING() ) {
            ( $continues, $prev ) = ( 1, eval { $q->[1]->streamUrl } // '' );
        }
    }
    my ( $follows, $next ) = following( $client, $c );
    return write_note( $t->{clientid},
        { continues => $continues, prev => $prev, this => kind( eval { $song->currentTrack } ), follows => $follows, next => $next,
          exact => exactLength($client) } );
}

# The engine's report on a player's last stream (engine length.go, length_<id>): what it stated and sent, and, if it held
# for a next track, the frames of the tail it kept (tailFrames); undef without one
sub lengthReport {
    my $id  = shift;
    my $dir = $Plugins::SqueezeDSP::Plugin::pluginTempDataDir;
    return undef unless defined $dir && length $dir && defined $id && length $id;
    ( my $clean = $id ) =~ tr/:-/__/;
    open( my $fh, '<', File::Spec->catfile( $dir, "length_$clean" ) ) or return undef;
    local $/;
    my $raw = <$fh>;
    close $fh;
    my $j = eval { JSON::XS::decode_json($raw) };
    return ref $j eq 'HASH' ? $j : undef;
}

# The held tail of a player's last stream, as a stream of its own, when the track it held for went (the exact contract:
# the renderer was told that stream's length at its start, so the tail cannot join it; nothing queues it): { stages => the
# engine's stage with --tail and every stage after it (FLAC out's flac), each an argv; command => the same for /bin/sh;
# frames => the tail's frames; rate => at that rate }. undef: the last stream not ours, still running, or it did not hold.
# The engine plays the tail from the residual, once; a next track that does come takes the residual instead.
sub tail {
    my $client = shift;
    my $id = ref $client ? eval { $client->id } : $client;
    return undef unless defined $id && defined $LAST{$id};
    my $rep = lengthReport($id) or return undef;
    return undef unless $rep->{ended} && $rep->{held} && ( $rep->{tailFrames} // 0 ) > 0;
    require Plugins::SqueezeDSP::Status;
    my @stages = Plugins::SqueezeDSP::Status::stagesOf( $LAST{$id} );
    my $want = '--clientid=' . lc $id;
    my ($at) = grep { my $argv = $stages[$_]; grep { lc($_) eq $want } @$argv } 0 .. $#stages;
    return undef unless defined $at;
    my @out = map { [@$_] } @stages[ $at .. $#stages ];
    push @{ $out[0] }, '--tail';
    return { stages => \@out, command => join( ' | ', map { join ' ', map { _sh($_) } @$_ } @out ),
             frames => $rep->{tailFrames}, rate => $rep->{rate} };
}

# a word for /bin/sh: single-quoted, its own single quotes as '\''
sub _sh {
    ( my $w = shift ) =~ s/'/'\\''/g;
    return "'$w'";
}

# A player's playlist changed: its note, if one of our streams wrote one, says again what follows the streaming song
sub playlistChanged {
    my $request = shift;
    my $client  = eval { $request->client } or return;
    my $c       = $client->controller or return;
    my @players = eval { $c->allPlayers };
    @players = ($client) unless @players;
    for my $p (@players) {
        my $n = read_note( $p->id ) or next;
        my ( $follows, $next ) = following( $p, $c );
        next if ( $n->{follows} // '' ) eq $follows && ( $n->{next} // '' ) eq $next;
        @$n{qw(follows next)} = ( $follows, $next );
        write_note( $p->id, $n );
    }
}

sub install {
    Slim::Control::Request::subscribe( \&playlistChanged, [ ['playlist'] ] );
}

sub uninstall {
    Slim::Control::Request::unsubscribe( \&playlistChanged );
}

1;
